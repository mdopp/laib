#!/usr/bin/env python3
"""One-shot draft writer: ServiceBay config + templates → Laib services/ (ADR 0010).

Reads ServiceBay's config.json and template folders ONCE and writes, per
installed template, a draft services/<name>/laib.yml and values.yml into the
Laib state repo. It never touches the box, never reads secrets (values of
kind "secret" become `!secret NAME` references), and prints everything it
could not map. The Quadlet files are written by hand/agent per service.

Usage:
  migrate-servicebay.py --config config.json --templates <dir>[,<dir>...] --out <state repo> [--dry-run]
"""
import argparse, json, os, re, sys

def load_variables(template_dirs, name):
    for d in template_dirs:
        p = os.path.join(d, name, "variables.json")
        if os.path.exists(p):
            with open(p) as f:
                return json.load(f), p
    return None, None

def backup_subdirs(template_dirs, name):
    """dataSubdir entries of the template's servicebay.backup annotation, by regex
    (no PyYAML dependency; the annotation is a YAML block inside Pod YAML)."""
    for d in template_dirs:
        p = os.path.join(d, name, "template.yml")
        if os.path.exists(p):
            text = open(p).read()
            if "servicebay.backup" not in text:
                return None
            return re.findall(r"dataSubdir:\s*([^\s#]+)", text)
    return None

def yaml_str(s):
    return json.dumps(s)  # JSON strings are valid YAML scalars

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--config", required=True)
    ap.add_argument("--templates", required=True, help="comma-separated template roots")
    ap.add_argument("--out", required=True, help="Laib state repo")
    ap.add_argument("--dry-run", action="store_true")
    a = ap.parse_args()
    cfg = json.load(open(a.config))
    tdirs = a.templates.split(",")
    rp = cfg.get("reverseProxy", {})
    domain = rp.get("publicDomain", "")
    hosts_by_service = {}
    for h in rp.get("hosts", []):
        hosts_by_service.setdefault(h["service"], []).append(h)
    vars_by_service = {}
    for v in cfg.get("installedVariables", []):
        vars_by_service.setdefault(v.get("service", "_global"), []).append(v)

    unmapped = []
    written = []
    for name, inst in sorted(cfg.get("installedTemplates", {}).items()):
        decl, src = load_variables(tdirs, name)
        if decl is None:
            unmapped.append(f"{name}: no variables.json in {tdirs} (external registry? copy its folder next to the built-ins)")
            decl = {}
        routes, secrets, ports, values = [], [], [], {}
        for var, d in decl.items():
            t = d.get("type")
            if t == "secret":
                secrets.append({"name": var, "external": bool(d.get("noAutoGenerate"))})
                values[var] = f"!secret {var}"
            elif t == "subdomain":
                port_var = d.get("proxyPort")
                port = None
                for h in hosts_by_service.get(name, []):
                    if h["domain"].split(".")[0] == (d.get("default") or ""):
                        port = h["forwardPort"]
                if port is None and port_var:
                    for v in vars_by_service.get(name, []):
                        if v["varName"] == port_var and v.get("value"):
                            port = int(v["value"])
                routes.append({
                    "host": d.get("default", var.lower()), "port": port or 0,
                    "exposure": d.get("exposure", "internal"),
                    "auth": "none" if d.get("audience") == "public-anon" else "one_factor",
                    "_from": var,
                })
            if d.get("blockLanAccess") and t == "text":
                ports.append({"port_var": var})
        # values from the box (non-secret)
        for v in vars_by_service.get(name, []):
            if v.get("kind") == "secret":
                continue
            values[v["varName"]] = v.get("value", "")
        # port numbers for blockLanAccess entries
        for p in ports:
            val = values.get(p["port_var"]) or decl.get(p["port_var"], {}).get("default")
            p["port"] = int(val) if val and str(val).isdigit() else 0
        # routes the box has that the template did not declare as subdomain vars
        declared_hosts = {r["host"] for r in routes}
        for h in hosts_by_service.get(name, []):
            sub = h["domain"][: -len(domain) - 1] if h["domain"].endswith("." + domain) else h["domain"]
            if sub not in declared_hosts and sub != domain:
                routes.append({"host": sub, "port": h["forwardPort"], "exposure": h.get("exposure", "internal"), "auth": "one_factor", "_from": "reverseProxy.hosts"})
        for r in routes:
            if r["port"] == 0:
                unmapped.append(f"{name}: route {r['host']} has no port (from {r['_from']})")

        laib = [f"name: {name}", f"template: {{registry: servicebay, name: {name}, version: {inst.get('schemaVersion', 1)}}}", "migratedFrom: servicebay"]
        laib.append("routes:" if routes else "routes: []")
        for r in routes:
            laib.append(f"  - {{host: {r['host']}, port: {r['port']}, exposure: {r['exposure']}, auth: {r['auth']}}}")
        laib.append("secrets:" if secrets else "secrets: []")
        for s_ in secrets:
            laib.append(f"  - {{name: {s_['name']}" + (", external: true}" if s_["external"] else "}"))
        subdirs = backup_subdirs(tdirs, name)
        if subdirs:
            laib.append("data:            # from the template's servicebay.backup annotation; review")
            for sd in subdirs:
                laib.append("  - path: ${DATA_DIR}/" + sd)
        elif subdirs is None:
            laib.append("data:            # TODO: the template declared no backup; declare paths or `data: []` with a reason")
            laib.append("  - path: ${DATA_DIR}/" + name)
            unmapped.append(f"{name}: no servicebay.backup annotation; data[] is a guess")
        else:
            laib.append("data: []         # template declared servicebay.backup without stores")
        laib.append("health: {}       # TODO: http health URL")
        laib.append("ports:" if ports else "ports: []")
        for p in ports:
            laib.append(f"  - {{port: {p['port']}, lan: false}}")
        vals = [f"{k}: {v}" if str(v).startswith("!secret ") else f"{k}: {yaml_str(str(v))}" for k, v in sorted(values.items())]
        written.append((name, "\n".join(laib) + "\n", "\n".join(vals) + "\n"))

    print(f"domain: {domain}  lanIp: {rp.get('lanIp','')}  services: {len(written)}")
    for name, laib, vals in written:
        print(f"  {name}: {laib.count('- {host')} routes, {laib.count('- {name')} secrets, {len(vals.splitlines())} values")
        if not a.dry_run:
            d = os.path.join(a.out, "services", name)
            os.makedirs(d, exist_ok=True)
            open(os.path.join(d, "laib.yml"), "w").write(laib)
            open(os.path.join(d, "values.yml"), "w").write(vals)
    if unmapped:
        print("\nunmapped:")
        for u in unmapped:
            print("  -", u)
    return 0

if __name__ == "__main__":
    sys.exit(main())
