#!/usr/bin/env python3
"""caddy's detail line for the Box page (#785): how many addresses Caddy
serves and until when the certificates it hands out are valid.

The addresses come from Caddy's running config (its admin API on the host's
127.0.0.1:2019, /config/apps/http/servers): every host name a route matches,
the wildcard catch-all left out. The config carries the proxy secrets; this
reads the host names only and prints nothing else of it. For each address
one TLS handshake to 127.0.0.1:443 with its name, as a phone sees it, reads
the served certificate's end, unverified (a box with tls: internal has
Caddy's own CA). Only reads. A silent admin API is "nicht gemessen", a
certificate ending within WARN_DAYS or an address without one a yellow
finding.

Environment: CADDY_ADMIN, TLS_HOST, TLS_PORT, WARN_DAYS, STATUS_DIR.
"""
import concurrent.futures, datetime, json, os, socket, ssl, tempfile, time, urllib.request

ADMIN = os.environ.get("CADDY_ADMIN", "http://127.0.0.1:2019").rstrip("/")
TLS_HOST, TLS_PORT = os.environ.get("TLS_HOST", "127.0.0.1"), int(os.environ.get("TLS_PORT", "443"))
WARN_DAYS = int(os.environ.get("WARN_DAYS", "14"))
STATUS = os.path.join(os.environ.get("STATUS_DIR", "/data"), "laib-status.json")


def status(detail, findings=()):
    s = {"at": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), "detail": detail, "findings": list(findings)}
    os.makedirs(os.path.dirname(STATUS), exist_ok=True)
    with open(STATUS + ".tmp", "w") as f:
        json.dump(s, f, ensure_ascii=False, indent=1)
    os.replace(STATUS + ".tmp", STATUS)


def hosts(servers):
    out = set()
    for srv in (servers or {}).values():
        for route in srv.get("routes") or []:
            for m in route.get("match") or []:
                out.update(h for h in m.get("host") or [] if "*" not in h)
    return sorted(out)


def not_after(name):
    """the end of the certificate Caddy serves for name, in epoch seconds"""
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
    ctx.check_hostname, ctx.verify_mode = False, ssl.CERT_NONE
    with socket.create_connection((TLS_HOST, TLS_PORT), timeout=10) as raw, ctx.wrap_socket(raw, server_hostname=name) as s:
        der = s.getpeercert(binary_form=True)
    if not der:
        raise ssl.SSLError("kein Zertifikat")
    with tempfile.NamedTemporaryFile("w", suffix=".pem") as f:
        f.write(ssl.DER_cert_to_PEM_cert(der))
        f.flush()
        return ssl.cert_time_to_seconds(ssl._ssl._test_decode_cert(f.name)["notAfter"])


def main():
    try:
        with urllib.request.urlopen(ADMIN + "/config/apps/http/servers", timeout=10) as r:
            names = hosts(json.loads(r.read() or b"null"))
    except (OSError, ValueError, AttributeError) as e:
        return status("nicht gemessen: Caddys Admin-Schnittstelle antwortet nicht (" + type(e).__name__ + ")",
                      [{"severity": "info", "unmeasured": True, "detail": "Caddys Adressen und Zertifikate nicht gemessen: die Admin-Schnittstelle auf 127.0.0.1:2019 antwortet nicht",
                        "fix": "laib inspect caddy"}])
    ends, missing = {}, []
    with concurrent.futures.ThreadPoolExecutor(8) as pool:
        for name, fut in [(n, pool.submit(not_after, n)) for n in names]:
            try:
                ends[name] = fut.result()
            except (OSError, ValueError, KeyError, AttributeError) as e:
                missing.append((name, str(e) or type(e).__name__))
    findings = []
    detail = f"{len(names)} Adressen"
    if ends:
        first = min(ends.values())
        detail += " · Zertifikate gültig bis " + time.strftime("%d.%m.%Y", time.gmtime(first))
        soon = sorted(n for n, t in ends.items() if t - time.time() < WARN_DAYS * 86400)
        if soon:
            days = max(int((first - time.time()) // 86400), 0)
            findings.append({"severity": "gelb", "detail": f"Das Zertifikat für {', '.join(soon)} läuft in {days} Tagen ab und ist noch nicht erneuert",
                             "fix": "laib logs caddy (die Erneuerung: ACME, DNS-Eintrag, Port 80); laib diag"})
    if missing:
        detail += f", {len(missing)} ohne Zertifikat"
        findings.append({"severity": "gelb", "detail": "Ohne Zertifikat: " + ", ".join(f"{n} ({why})" for n, why in missing),
                         "fix": "laib logs caddy; laib diag"})
    status(detail, findings)


if __name__ == "__main__":
    main()
