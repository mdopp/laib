#!/usr/bin/env python3
"""One-shot draft writer: ServiceBay config + templates → Laib drafts (ADR 0010).

Reads ServiceBay's config.json and template folders ONCE and writes, per
installed template, a draft <out>/<name>/laib.yml and values.yml for review.
It never touches the box and never reads a secret value: a secret-typed
variable becomes `!secret NAME`. It prints everything it could not map, and
the secrets a Laib built-in needs carried over before its first apply.

The drafts never go into the state repo: a services/<name> without units makes
laib refuse the whole state, and `laib add <name>` refuses one that exists
(#226). Where Laib has a built-in, the draft is the ServiceBay side to compare
with what `laib add <name>` writes.

Usage:
  migrate-servicebay.py --config config.json --templates <dir>[,<dir>...] --out <dir>
                        [--builtins <laib>/templates] [--dry-run]
"""
import argparse, json, os, re, sys

SECRET_TYPES = {"secret", "bcrypt", "rsa-private"}   # ServiceBay's own set (install/savedSecrets.ts)
FORWARD_AUTH = "__authelia_forward_auth__"

# Secrets whose value the existing data was made with: a new value locks the
# app out of its own database or config (#228). ServiceBay name → Laib name,
# per service; the one table is carry-servicebay-secrets.sh's, which sets them.
CARRY_SCRIPT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "carry-servicebay-secrets.sh")
CARRY = {}
for _line in open(CARRY_SCRIPT).read().split("TABLE='", 1)[1].split("'", 1)[0].splitlines():
    if len(_line.split()) == 3:
        _svc, _sb, _laib = _line.split()
        CARRY.setdefault(_svc, {})[_sb] = _laib


# Values the box set under ServiceBay's name that Laib's built-in takes under
# its own, for the `laib add` line (#229). A blank LLDAP_BASE_DN is the one
# ServiceBay derived from the domain.
VALUES = {
    "auth": {"LLDAP_BASE_DN": "BASE_DN"},
    "radicale": {"LLDAP_BASE_DN": "BASE_DN"},
    "media": {"LLDAP_BASE_DN": "BASE_DN"},   # the LDAP plugin's search base (#256)
    "mosquitto": {"MQTT_USERNAME": "MQTT_USER"},
    "file-share": {"SHARE_USER": "SHARE_USER"},
}
PIN = "$(podman image inspect --format '{{index .RepoDigests 0}}' %s)"


def builtin_values(builtins, name):
    """values.example.yml's literals, and the image variables Laib updates itself (AutoUpdate=)."""
    d = os.path.join(builtins, name)
    vals = dict(re.findall(r"(?m)^([A-Z0-9_]+):\s*([^\s#!][^\s#]*)", open(os.path.join(d, "values.example.yml")).read()))
    auto = set()
    for f in os.listdir(d):
        if f.endswith(".container"):
            t = open(os.path.join(d, f)).read()
            m = re.search(r"(?m)^Image=\$\{([A-Z0-9_]+)\}", t)
            if m and re.search(r"(?m)^AutoUpdate=", t):
                auto.add(m.group(1))
    return vals, auto


def find(template_dirs, name, filename):
    for d in template_dirs:
        p = os.path.join(d, name, filename)
        if os.path.exists(p):
            return p
    return None


def volumes(text):
    """hostPath paths and PVC claim names of a Pod YAML, by regex (no PyYAML)."""
    return re.findall(r"hostPath:\s*\n\s*path:\s*([^\s#]+)", text), re.findall(r"claimName:\s*([^\s#]+)", text)


def resolve(expr, values, data_dir):
    p = re.sub(r"\{\{\s*([A-Z0-9_]+)\s*\}\}",
               lambda m: "${DATA_DIR}" if m.group(1) == "DATA_DIR" else values.get(m.group(1), m.group(0)), expr)
    return "${DATA_DIR}" + p[len(data_dir):] if p.startswith(data_dir + "/") else p


def in_state_repo(out):
    out = os.path.abspath(out)
    return any(os.path.exists(os.path.join(d, "box.yml")) for d in (out, os.path.dirname(out)))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--config", required=True)
    ap.add_argument("--templates", required=True, help="comma-separated template roots")
    ap.add_argument("--out", required=True, help="a review directory, never the state repo")
    ap.add_argument("--builtins", default=os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "templates"))
    ap.add_argument("--dry-run", action="store_true")
    a = ap.parse_args()
    if in_state_repo(a.out):
        print(f"{a.out} is (in) a Laib state repo: drafts go to a review directory; laib refuses "
              "a services/<name> without units, and laib add refuses one that exists", file=sys.stderr)
        return 1
    cfg = json.load(open(a.config))
    tdirs = a.templates.split(",")
    rp = cfg.get("reverseProxy", {})
    domain = rp.get("publicDomain", "")
    data_dir = cfg.get("templateSettings", {}).get("DATA_DIR", "/mnt/data/stacks").rstrip("/")
    installed = cfg.get("installedTemplates", {})
    unmapped, carry, adds = [], [], []

    hosts_by_service = {}
    for h in rp.get("hosts", []):
        if h["service"] not in installed:
            unmapped.append(f"proxy host {h['domain']} → {h['service']}:{h['forwardPort']}: not an installed template; carry it by hand or drop it")
            continue
        sub = h["domain"][: -len(domain) - 1] if h["domain"].endswith("." + domain) else h["domain"]
        hosts_by_service.setdefault(h["service"], {})[sub] = h
    vars_by_service, globals_ = {}, {}
    for v in cfg.get("installedVariables", []):
        s = v.get("service")
        if s is None:
            globals_[v["varName"]] = v
        elif s in installed:
            vars_by_service.setdefault(s, []).append(v)
        else:
            unmapped.append(f"variable {v['varName']} of {s}: not an installed template; dropped")
    used_globals = set()

    written, owners = [], {}
    for name, inst in sorted(installed.items()):
        vpath = find(tdirs, name, "variables.json")
        if vpath is None:
            unmapped.append(f"{name}: no variables.json in {tdirs} (external registry? copy its folder next to the built-ins)")
        decl = json.load(open(vpath)) if vpath else {}
        builtin = os.path.exists(os.path.join(a.builtins, name, "laib.yml"))
        # values: the box's non-secret values, then the service-less ones the template declares
        values = {}
        for v in vars_by_service.get(name, []):
            if v.get("kind") != "secret" and decl.get(v["varName"], {}).get("type") not in SECRET_TYPES:
                values[v["varName"]] = v.get("value", "")
        for k, v in globals_.items():
            if k in decl:
                values[k] = v.get("value", "")
                used_globals.add(k)
        resolved = {k: d["default"] for k, d in decl.items() if d.get("default") not in (None, "")}
        resolved.update({k: v for k, v in values.items() if v})

        routes, secrets, ports = [], [], []
        box_hosts = dict(hosts_by_service.get(name, {}))
        for var, d in decl.items():
            t = d.get("type")
            if t in SECRET_TYPES:
                secrets.append({"name": var, "external": bool(d.get("noAutoGenerate"))})
                values[var] = f"!secret {var}"
            elif t == "subdomain":
                host = values.get(var) or d.get("default") or var.lower()
                h = box_hosts.pop(host, None)
                if h is None:
                    unmapped.append(f"{name}: {var} ({host}) is declared but the box has no proxy host for it; left out")
                    continue
                fa = FORWARD_AUTH in (d.get("proxyConfig", {}).get("advanced_config") or "")
                routes.append({"host": host, "port": h["forwardPort"], "exposure": h.get("exposure", d.get("exposure", "internal")),
                               "auth": "one_factor" if fa else "none",
                               "group": "admins" if fa and d.get("audience") == "admin" else None})
            if d.get("blockLanAccess"):
                val = str(values.get(var, ""))
                if not val.isdigit():
                    if val:
                        unmapped.append(f"{name}: {var} is {val!r}, not a port; used the template default")
                    val = str(d.get("default", ""))
                ports.append(int(val) if val.isdigit() else 0)
        for host, h in sorted(box_hosts.items()):
            unmapped.append(f"{name}: route {host} exists only as a proxy host (no template variable); auth one_factor is a guess")
            routes.append({"host": host, "port": h["forwardPort"], "exposure": h.get("exposure", "internal"), "auth": "one_factor", "group": None})
        for r in routes:
            owners.setdefault(r["host"], []).append(name)

        # data: the Pod YAML's host paths under DATA_DIR that are the service's own
        data, shared = [], []
        tpath = find(tdirs, name, "template.yml")
        paths, claims = volumes(open(tpath).read()) if tpath else ([], [])
        for expr in paths:
            p = resolve(expr, resolved, data_dir)
            if p.startswith("${DATA_DIR}/"):
                (data if p.split("/")[1] == name else shared).append(p)
            elif "{{" in p:
                unmapped.append(f"{name}: host path {expr} has no value; declare it by hand")
            elif not p.startswith(("/run/", "/dev/", "/sys/", "/etc/")):
                unmapped.append(f"{name}: host path {p} is outside DATA_DIR; not a data path")
        for c in claims:
            unmapped.append(f"{name}: podman named volume {c} is not under DATA_DIR; copy it to a data path before the adoption")
        if not tpath:
            unmapped.append(f"{name}: no template.yml; data[] is a guess")
            data.append("${DATA_DIR}/" + name)
        if builtin and tpath:   # where the built-in would start on an empty directory (#229)
            mine = re.findall(r"-\s*path:\s*(\S+)", open(os.path.join(a.builtins, name, "laib.yml")).read())
            for p in dict.fromkeys(data):
                if p not in mine:
                    unmapped.append(f"{name}: the box keeps data in {p}; the built-in does not mount it (scripts/move-servicebay-data.sh {name} copies what has a place in Laib's layout, docs/MIGRATION.md #229)")
        for sb, laib in CARRY.get(name, {}).items():
            if sb in decl:
                carry.append((name, f"laib secret set {laib}   # ServiceBay: {sb} ({name})"))

        if builtin:   # the laib add line: the box's values, and a database's image pinned to what the box runs (#229)
            bvals, auto = builtin_values(a.builtins, name)
            add, pins = [], []
            for sb, key in VALUES.get(name, {}).items():
                v = str(values.get(sb) or decl.get(sb, {}).get("default") or "")
                if sb == "LLDAP_BASE_DN" and not v:
                    v = ",".join("dc=" + p for p in domain.split("."))
                if v and v != bvals.get(key):
                    add.append(f"{key}={v}")
            for img in re.findall(r"(?m)^\s*image:\s*(\S+)", open(tpath).read()) if tpath else []:
                img = re.sub(r"\{\{\s*([A-Z0-9_]+)\s*\}\}", lambda m: str(resolved.get(m.group(1), m.group(0))), img)
                repo, _, tag = img.rpartition(":")
                for key, v in sorted(bvals.items()):
                    if key not in auto and v.rpartition(":")[0] == repo and v.rpartition(":")[2] != tag:
                        pins.append(f"{key}={PIN % img} && ")
                        add.append(f"{key}=${key}")
            # an image without a RepoDigest (or gone) fails the $(…), and laib
            # add does not run with a blank image: it would take the default
            adds.append("".join(pins) + " ".join([f"laib add {name}"] + add))
        head = f"# Laib has a built-in: `laib add {name}`, then compare with this draft.\n" if builtin else ""
        tmpl = f"{{registry: builtin, name: {name}}}" if builtin else f"{{registry: servicebay, name: {name}, version: {inst.get('schemaVersion', 1)}}}"
        laib = [f"{head}name: {name}", f"template: {tmpl}", "routes:" if routes else "routes: []"]
        for r in routes:
            g = f", group: {r['group']}" if r["group"] else ""
            laib.append(f"  - {{host: {r['host']}, port: {r['port']}, exposure: {r['exposure']}, auth: {r['auth']}{g}}}")
        laib.append("secrets:" if secrets else "secrets: []")
        laib += [f"  - {{name: {s_['name']}" + (", external: true}" if s_["external"] else "}") for s_ in secrets]
        laib.append("data:" if data else "data: []         # the template mounts nothing of its own")
        laib += [f"  - path: {p}" for p in dict.fromkeys(data)]
        laib += [f"  # shared, not this service's own: {p}" for p in dict.fromkeys(shared)]
        laib.append("health: {}       # TODO: http health URL")
        laib.append("ports:" if ports else "ports: []")
        laib += [f"  - {{port: {p}, lan: false}}" for p in ports]
        vals = [f"{k}: {v}" if str(v).startswith("!secret ") else f"{k}: {json.dumps(str(v))}" for k, v in sorted(values.items())]
        written.append((name, builtin, len(routes), len(secrets), "\n".join(laib) + "\n", "\n".join(vals) + "\n"))

    for host, names in sorted(owners.items()):
        if len(names) > 1:
            unmapped.append(f"host {host} is claimed by {', '.join(names)}")
    for k in sorted(set(globals_) - used_globals):
        unmapped.append(f"variable {k} has no service and no installed template declares it; dropped")

    print(f"domain: {domain}  dataDir: {data_dir}  services: {len(written)}")
    for name, builtin, nr, ns, laib, vals in written:
        print(f"  {name}: {nr} routes, {ns} secrets, {len(vals.splitlines())} values" + ("  (Laib built-in)" if builtin else ""))
        if not a.dry_run:
            d = os.path.join(a.out, name)
            os.makedirs(d, exist_ok=True)
            open(os.path.join(d, "laib.yml"), "w").write(laib)
            open(os.path.join(d, "values.yml"), "w").write(vals)
    if carry:
        print("\ncarry over before the first apply (the data was made with these), on the box as core while")
        print("ServiceBay still runs: scripts/carry-servicebay-secrets.sh " + " ".join(dict.fromkeys(n for n, _ in carry)))
        for _, c in carry:
            print("  " + c)
    if adds:
        print("\nlaib add, once ServiceBay's unit is out and the data moved (scripts/move-servicebay-data.sh);")
        print("the box's values, and each database's image pinned to the digest the box runs until a deliberate update:")
        for l in adds:
            print("  " + l)
    if unmapped:
        print("\nunmapped:")
        for u in unmapped:
            print("  -", u)
    return 0


if __name__ == "__main__":
    sys.exit(main())
