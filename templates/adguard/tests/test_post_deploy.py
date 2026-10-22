"""#551 (2): AdGuard asks the LAN router for the names of private addresses.

AdGuard's default is use_private_ptr_resolvers: true with no
local_ptr_upstreams, so a PTR query for a LAN address goes to the system
resolver; on the box that is AdGuard itself, and every such query ran into
the timeout (~7000 in 48 h on the Kraftwerk). post-deploy now sets the
router (the box's default gateway, read from the host's routing table on
every apply) when the list is empty; PTR_UPSTREAMS, a value, wins over it.
An owner's own list in the UI stays. The fake keeps AdGuard's API habits:
dns_config takes a partial body and changes only the fields it names."""
import base64, json, os, subprocess, sys, tempfile, threading, unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

HOOK = Path(__file__).resolve().parent.parent / "post-deploy"
PW = "adg-pw-Q7x"
AUTH = "Basic " + base64.b64encode(f"admin:{PW}".encode()).decode()
AG = {}
HEADER = "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n"
# 192.168.178.1 as the kernel writes it: little-endian hex
ROUTES = HEADER + "podman0\t0058A8C0\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n" \
                  "eno1\t00000000\t01B2A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n" \
                  "eno1\t00B2A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n"


def fresh(**dns):
    AG.clear()
    AG.update(rewrites=[], posts=[], dns={"upstream_dns": ["https://dns.quad9.net/dns-query"], "local_ptr_upstreams": [],
                                          "use_private_ptr_resolvers": True, **dns})


class FakeAdGuard(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def reply(self, code, body=None):
        data = json.dumps(body).encode() if body is not None else b""
        self.send_response(code)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if self.headers.get("Authorization") != AUTH:
            return self.reply(401)
        if self.path == "/control/rewrite/list":
            return self.reply(200, AG["rewrites"])
        if self.path == "/control/dns_info":
            return self.reply(200, AG["dns"])
        self.reply(404)

    def do_POST(self):
        if self.headers.get("Authorization") != AUTH:
            return self.reply(401)
        b = json.loads(self.rfile.read(int(self.headers.get("Content-Length") or 0)) or b"{}")
        if self.path == "/control/rewrite/add":
            AG["rewrites"].append(b)
            return self.reply(200)
        if self.path == "/control/dns_config":
            AG["posts"].append(b)
            AG["dns"].update(b)                     # a partial body: only the named fields change
            return self.reply(200)
        self.reply(404)


class PostDeploy(unittest.TestCase):
    def setUp(self):
        self.srv = HTTPServer(("127.0.0.1", 0), FakeAdGuard)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.addCleanup(self.srv.shutdown)
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        self.render = Path(self.dir.name) / "render"
        (self.render / "adguard").mkdir(parents=True)
        (self.render / "adguard" / "rewrites.json").write_text(json.dumps([{"domain": "*.vm.test", "answer": "192.168.178.2"}]))
        self.routes = Path(self.dir.name) / "route"
        self.routes.write_text(ROUTES)

    def run_hook(self, **extra):
        env = dict(os.environ, ADGUARD_URL=f"http://127.0.0.1:{self.srv.server_port}", LAIB_DOMAIN="vm.test",
                   LAIB_RENDER_DIR=str(self.render), ROUTE_TABLE=str(self.routes), ADMIN_USER="admin",
                   LAIB_SECRET_ADGUARD_ADMIN_PASSWORD=PW, **extra)
        p = subprocess.run([sys.executable, str(HOOK)], env=env, capture_output=True, text=True, timeout=60)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertNotIn(PW, p.stdout + p.stderr, "the admin password is in the hook's output, which goes to the journal")
        return p

    def test_an_empty_list_gets_the_router_and_the_rewrites_still_sync(self):
        fresh()
        self.run_hook()
        self.assertEqual(AG["dns"]["local_ptr_upstreams"], ["192.168.178.1"], "PTR for LAN addresses still goes nowhere")
        self.assertTrue(AG["dns"]["use_private_ptr_resolvers"])
        self.assertEqual(AG["posts"], [{"local_ptr_upstreams": ["192.168.178.1"]}], "dns_config must name only the field it sets")
        self.assertEqual(AG["rewrites"], [{"domain": "*.vm.test", "answer": "192.168.178.2"}])
        self.run_hook()
        self.assertEqual(len(AG["posts"]), 1, "an unchanged list was written again")

    def test_without_rendered_rewrites_the_router_is_still_set(self):
        fresh()
        (self.render / "adguard" / "rewrites.json").unlink()
        self.run_hook()
        self.assertEqual(AG["dns"]["local_ptr_upstreams"], ["192.168.178.1"])

    def test_the_owners_own_list_stays(self):
        fresh(local_ptr_upstreams=["10.0.0.53"])
        self.run_hook()
        self.assertEqual(AG["posts"], [], "the list the owner set in AdGuard's UI was overwritten")

    def test_the_value_wins_over_the_gateway_and_over_the_ui(self):
        fresh(local_ptr_upstreams=["10.0.0.53"])
        self.run_hook(PTR_UPSTREAMS="192.168.178.5 192.168.178.6")
        self.assertEqual(AG["dns"]["local_ptr_upstreams"], ["192.168.178.5", "192.168.178.6"])

    def test_no_default_route_sets_nothing_and_says_so(self):
        fresh()
        self.routes.write_text(HEADER)
        p = self.run_hook()
        self.assertEqual(AG["posts"], [])
        self.assertIn("PTR", p.stdout + p.stderr)


if __name__ == "__main__":
    unittest.main()
