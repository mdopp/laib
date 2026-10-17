#!/usr/bin/env python3
"""`laib-app`: the mechanics of a small Werkstatt app (#262), so an agent
runs commands instead of re-deriving them from prose.

  laib-app new <name> [--repo DIR] [--owner O] [--port N] [--group family|admins] [--exposure internal|public]
      an app (Go, tests, Containerfile) and its Laib template in the app
      registry repo; the repo itself (CI, README, AGENTS.md) on first use
  laib-app register <git-url> ["<grund>"]
      propose the repo as the box's template registry (box.yml registries), once
  laib-app check <name>.<domain> [--cafile F] [--open]
      is the route done: public DNS, the certificate, the login. Exit 0 done,
      8 a check failed (the output says what to do), 9 could not measure

The guide is /opt/werkstatt/guide/apps.md.
"""
import argparse, json, os, re, shutil, socket, ssl, subprocess, sys, time, urllib.parse, urllib.request, zlib
import http.client
from pathlib import Path

SCAFFOLD = Path(os.environ.get("LAIB_APP_SCAFFOLD") or "/opt/werkstatt/scaffold")
DEFAULT_REPO = os.environ.get("LAIB_APP_REPO") or "/workspace/laib-apps"
REGISTRY = "apps"
DOH = os.environ.get("LAIB_APP_DOH") or "https://cloudflare-dns.com/dns-query"
BOX = os.environ.get("LAIB_APP_BOX") or "host.containers.internal"   # Caddy, on the box's host network
NAME_RE = re.compile(r"^[a-z][a-z0-9]{1,29}$")
DONE, FAILED, UNMEASURED = 0, 8, 9


def die(msg: str, code: int = 1) -> int:
    print("laib-app: " + msg, file=sys.stderr)
    return code


def owner_of(repo: Path) -> str:
    try:
        url = subprocess.run(["git", "-C", str(repo), "remote", "get-url", "origin"], capture_output=True, text=True).stdout
    except OSError:
        url = ""
    m = re.search(r"github\.com[:/]([^/]+)/", url)
    return m.group(1) if m else (os.environ.get("GITHUB_OWNER") or "mdopp")


def fill(src: Path, dst: Path, subs: dict) -> list[Path]:
    """Copy a scaffold tree, replacing __KEY__ in names and contents."""
    out = []
    for p in sorted(src.rglob("*")):
        rel = str(p.relative_to(src))
        for k, v in subs.items():
            rel = rel.replace(k, v)
        target = dst / rel
        if p.is_dir():
            target.mkdir(parents=True, exist_ok=True)
            continue
        text = p.read_text()
        for k, v in subs.items():
            text = text.replace(k, v)
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(text)
        out.append(target)
    return out


def cmd_new(a) -> int:
    name = a.name
    if not NAME_RE.match(name):
        return die(f"{name!r}: one lowercase word, letters and digits, 2-30 long (ADR 0021: a service is one noun)")
    repo = Path(a.repo)
    if (repo / "apps" / name).exists() or (repo / "templates" / name).exists():
        return die(f"{name} exists in {repo}; change it there instead of scaffolding again")
    fresh = not (repo / ".git").exists()
    if fresh:
        repo.mkdir(parents=True, exist_ok=True)
        subprocess.run(["git", "init", "-q", "-b", "main", str(repo)], check=True)
    owner = a.owner or owner_of(repo)
    port = a.port or 18000 + zlib.crc32(name.encode()) % 1000
    subs = {
        "__NAME__": name, "__Name__": name[:1].upper() + name[1:], "__PORT__": str(port),
        "__OWNER__": owner, "__REPO__": repo.name, "__REGISTRY__": REGISTRY,
        "__MODULE__": f"github.com/{owner}/{repo.name}/apps/{name}",
        "__SECRET__": name.upper() + "_PROXY_SECRET", "__AUTH__": "one_factor",
        "__GROUP__": a.group, "__EXPOSURE__": a.exposure,
    }
    written = []
    if fresh or not (repo / ".github" / "workflows" / "apps.yml").exists():
        written += fill(SCAFFOLD / "repo", repo, subs)
    written += fill(SCAFFOLD / "go", repo, subs)
    left = [str(p) for p in written if re.search(r"__[A-Za-z]+__", p.read_text())]
    if left:
        return die("placeholders left in " + ", ".join(left))
    print(f"{name}: app in apps/{name}, template in templates/{name} (port {port}, route {name}.<domain>, "
          f"{a.exposure}, login through Authelia for group {a.group})")
    print(f"""next:
  cd {repo}/apps/{name} && go test ./...
  cd {repo} && laib template lint templates/{name}
  git add -A && git commit -m "feat({name}): …" && push, open a PR; the owner merges, CI publishes ghcr.io/{owner}/{name}
  first app in this repo: laib-app register https://github.com/{owner}/{repo.name}
  then: laib template sync && laib propose --add {name} "<grund>"   (the guide: /opt/werkstatt/guide/apps.md)""")
    return 0


def registry_patch(url: str, ref: str) -> str:
    # zero context: inserted at the top of box.yml, which has no registries
    # yet (a second `registries:` key would not load, and the proposal says so)
    lines = ["registries:", f"  - name: {REGISTRY}", f"    url: {url}", f"    ref: {ref}"]
    return "--- a/box.yml\n+++ b/box.yml\n@@ -0,0 +1,%d @@\n" % len(lines) + "".join("+" + l + "\n" for l in lines)


def cmd_register(a) -> int:
    reason = a.reason or f"Vorlagen der Werkstatt-Programme aus {a.url}"
    p = subprocess.run(["laib", "propose", "--patch", reason], input=registry_patch(a.url, a.ref), capture_output=True, text=True)
    sys.stdout.write(p.stdout)
    sys.stderr.write(p.stderr)
    if p.returncode != 0 and "already" in (p.stdout + p.stderr):
        print(f"""box.yml has registries already: this one needs a line there, which only the owner can add.
Ask them to add under `registries:` in box.yml:
  - name: {REGISTRY}
    url: {a.url}
    ref: {a.ref}""", file=sys.stderr)
    return p.returncode


# ── check ────────────────────────────────────────────────────────────────

def doh(name: str, rtype: str) -> list[dict]:
    q = urllib.parse.urlencode({"name": name, "type": rtype})
    req = urllib.request.Request(f"{DOH}?{q}", headers={"Accept": "application/dns-json"})
    with urllib.request.urlopen(req, timeout=10) as r:
        body = json.load(r)
    return body.get("Answer") or []


def resolve(name: str) -> tuple[list[str], set[str]]:
    """(CNAME chain, A records) from public DNS."""
    answers = doh(name, "A")
    return [x["data"].rstrip(".") for x in answers if x.get("type") == 5], {x["data"] for x in answers if x.get("type") == 1}


def strato_line(fqdn: str, target: str) -> str:
    sub = fqdn[: -len(target) - 1] if fqdn.endswith("." + target) else fqdn
    line = f"Bei Strato unter {target} die Subdomain „{sub}“ anlegen und als CNAME-Ziel „{target}“ eintragen."
    if sub.count(".") > 0:   # a trial box under a sub-zone: port 80 reaches the Kraftwerk (ADR 0007, 2026-10-13)
        line += f" Dazu auf dem Kraftwerk im NPM einen Proxy-Host {fqdn}, nur HTTP (kein SSL), auf Port 80 dieser Box."
    return line


def check_dns(fqdn: str, target: str) -> tuple[int, str]:
    try:
        chain, addrs = resolve(fqdn)
        _, want = resolve(target)
    except (OSError, ValueError) as e:
        return UNMEASURED, f"public DNS could not be asked ({DOH}): {e}"
    if not addrs:
        return FAILED, f"{fqdn} does not resolve in public DNS. Owner: {strato_line(fqdn, target)} Then wait (Strato takes up to an hour) and check again."
    if want and not addrs & want:
        return FAILED, f"{fqdn} resolves to {sorted(addrs)}, {target} to {sorted(want)}: not the house. Owner: {strato_line(fqdn, target)}"
    via = f" via CNAME {' → '.join(chain)}" if chain else ""
    return DONE, f"{fqdn}{via} → {', '.join(sorted(addrs))}"


def tls_context(cafile: str | None) -> ssl.SSLContext:
    return ssl.create_default_context(cafile=cafile) if cafile else ssl.create_default_context()


def box_addr(box: str) -> tuple[str, int]:
    host, _, port = box.rpartition(":") if box.count(":") == 1 else (box, "", "")
    return (host, int(port)) if port else (box, 443)


def check_cert(fqdn: str, box: str, ctx: ssl.SSLContext) -> tuple[int, str]:
    try:
        with socket.create_connection(box_addr(box), timeout=10) as raw, ctx.wrap_socket(raw, server_hostname=fqdn) as s:
            cert = s.getpeercert()
    except ssl.SSLCertVerificationError as e:
        return FAILED, (f"no valid certificate for {fqdn} yet ({e.verify_message}). Caddy asks Let's Encrypt by HTTP-01 "
                        "once the name resolves and port 80 reaches the box; wait a few minutes, then `laib logs caddy --since 15m`")
    except (OSError, ssl.SSLError) as e:
        return UNMEASURED, f"Caddy did not answer on {box} for {fqdn}: {e}"
    start, end = ssl.cert_time_to_seconds(cert["notBefore"]), ssl.cert_time_to_seconds(cert["notAfter"])
    left = end - time.time()
    issuer = dict(x[0] for x in cert.get("issuer", ()))
    who = issuer.get("organizationName") or issuer.get("commonName", "?")
    # Caddy renews with a third of the lifetime left (Let's Encrypt: weeks, its own CA: hours)
    if left < (end - start) / 5:
        return FAILED, f"the certificate for {fqdn} ends in {left / 3600:.0f} hours ({who}): renewal is failing, `laib logs caddy`"
    return DONE, f"certificate for {fqdn} from {who}, valid for {left / 86400:.1f} more days"


class _Via(http.client.HTTPSConnection):
    """HTTPS to the box's Caddy, with the route's name as SNI and Host."""
    def __init__(self, fqdn, box, ctx):
        super().__init__(fqdn, 443, context=ctx, timeout=10)
        self._box, self._ctx = box, ctx

    def connect(self):
        self.sock = self._ctx.wrap_socket(socket.create_connection(box_addr(self._box), timeout=10), server_hostname=self.host)


def check_login(fqdn: str, box: str, ctx: ssl.SSLContext, open_route: bool) -> tuple[int, str]:
    domain = fqdn.split(".", 1)[1]
    try:
        c = _Via(fqdn, box, ctx)
        c.request("GET", "/", headers={"Remote-User": "laib-app-check"})   # a forged login must not get through
        r = c.getresponse()
        loc = r.getheader("Location") or ""
        body = r.read(400).decode("utf-8", "replace")
    except (OSError, ssl.SSLError, http.client.HTTPException) as e:
        return UNMEASURED, f"https://{fqdn}/ did not answer through {box}: {e}"
    if open_route:
        if r.status >= 500:
            return FAILED, f"https://{fqdn}/ answers {r.status}: the app is down (`laib status`, `laib logs`)"
        return DONE, f"https://{fqdn}/ answers {r.status} without Authelia (auth: none — the app logs in itself)"
    if r.status in (301, 302, 303, 307) and urllib.parse.urlparse(loc).hostname == "auth." + domain:
        return DONE, f"without a login https://{fqdn}/ sends to {urllib.parse.urlparse(loc).hostname} (Authelia)"
    if r.status == 401:
        return DONE, f"without a login https://{fqdn}/ answers 401 (Authelia)"
    if r.status == 403 and not loc:
        return FAILED, f"https://{fqdn}/ answers 403: an internal route seen from outside the home network, or the group is wrong"
    return FAILED, f"https://{fqdn}/ answers {r.status} {loc} without a login: the route is not behind Authelia ({body[:80]!r})"


def cmd_check(a) -> int:
    fqdn = a.fqdn.strip().rstrip(".").lower()
    if fqdn.count(".") < 2 and not a.target:
        return die(f"{fqdn}: expected <name>.<domain>")
    target = a.target or ".".join(fqdn.split(".")[-2:])
    ctx = tls_context(a.cafile)
    results = [("dns", check_dns(fqdn, target)), ("cert", check_cert(fqdn, a.box, ctx))]
    if results[1][1][0] == DONE:
        results.append(("login", check_login(fqdn, a.box, ctx, a.open)))
    else:
        results.append(("login", (UNMEASURED, "not asked: without a valid certificate no browser gets that far")))
    codes = set()
    for what, (code, msg) in results:
        print(f"{ {DONE: 'ok  ', FAILED: 'FAIL', UNMEASURED: '??  '}[code]} {what:<5} {msg}")
        codes.add(code)
    # a failed check says what to do, so it wins over one that could not be measured
    worst = FAILED if FAILED in codes else UNMEASURED if UNMEASURED in codes else DONE
    print({DONE: f"route done: https://{fqdn}/", FAILED: "route not done", UNMEASURED: "route not measured: say so, do not call it done"}[worst])
    return worst


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(prog="laib-app", description=__doc__.split("\n\n")[0])
    sub = ap.add_subparsers(dest="cmd", required=True)
    n = sub.add_parser("new")
    n.add_argument("name")
    n.add_argument("--repo", default=DEFAULT_REPO)
    n.add_argument("--owner")
    n.add_argument("--port", type=int)
    n.add_argument("--group", default="family", choices=["family", "admins"])
    n.add_argument("--exposure", default="internal", choices=["internal", "public"])
    r = sub.add_parser("register")
    r.add_argument("url")
    r.add_argument("reason", nargs="?")
    r.add_argument("--ref", default="main")
    c = sub.add_parser("check")
    c.add_argument("fqdn")
    c.add_argument("--box", default=BOX, help="where Caddy answers, host[:port] (default: the box, port 443)")
    c.add_argument("--cafile", default=os.environ.get("LAIB_APP_CAFILE"))
    c.add_argument("--target", help="the name the house's public IP has (default: the last two labels)")
    c.add_argument("--open", action="store_true", help="a route with auth: none")
    a = ap.parse_args(argv)
    return {"new": cmd_new, "register": cmd_register, "check": cmd_check}[a.cmd](a)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
