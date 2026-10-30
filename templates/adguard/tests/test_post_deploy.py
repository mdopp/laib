"""#551 (2): AdGuard asks the LAN router for the names of private addresses.

AdGuard's default is use_private_ptr_resolvers: true with no
local_ptr_upstreams, so a PTR query for a LAN address goes to the system
resolver; on the box that is AdGuard itself, and every such query ran into
the timeout (~7000 in 48 h on the Kraftwerk). post-deploy now sets the
router (the box's default gateway, read from the host's routing table on
every apply) when the list is empty; PTR_UPSTREAMS, a value, wins over it.
An owner's own list in the UI stays. The fake keeps AdGuard's API habits:
dns_config takes a partial body and changes only the fields it names.

#901: the router asked AdGuard for the PTR of 172.16.255.1 (~10,000 times),
AdGuard forwarded it to the router, the router asked AdGuard again, until
the 2 s timeout. post-deploy now keeps user rules that answer PTR for
172.16.0.0/12 with NXDOMAIN here (a dnsrewrite, so not counted as blocked),
beside the owner's own rules; and the query log keeps QUERYLOG_DAYS (7)
instead of AdGuard's 90 days. The fake checks what AdGuard checks: a query
log PUT needs enabled and anonymize_client_ip and an interval between an
hour and a year."""
import base64, json, os, re, subprocess, sys, tempfile, threading, unittest
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


DAY = 24 * 3600 * 1000
OURS = re.compile(r"^\|\|(\d+)\.172\.in-addr\.arpa\^\$dnstype=PTR,dnsrewrite=NXDOMAIN$")


def fresh(rules=(), **dns):
    AG.clear()
    AG.update(rewrites=[], posts=[], rules=list(rules), rule_posts=[], ql_puts=[],
              ql={"enabled": True, "interval": 90 * DAY, "anonymize_client_ip": False, "ignored": [], "ignored_enabled": False},
              dns={"upstream_dns": ["https://dns.quad9.net/dns-query"], "local_ptr_upstreams": [],
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
        if self.path == "/control/filtering/status":
            return self.reply(200, {"enabled": True, "interval": 24, "filters": [], "user_rules": AG["rules"]})
        if self.path == "/control/querylog/config":
            return self.reply(200, AG["ql"])
        self.reply(404)

    def do_PUT(self):
        if self.headers.get("Authorization") != AUTH:
            return self.reply(401)
        b = json.loads(self.rfile.read(int(self.headers.get("Content-Length") or 0)) or b"{}")
        if self.path == "/control/querylog/config/update":
            if b.get("enabled") is None or b.get("anonymize_client_ip") is None:
                return self.reply(422)              # AdGuard: "enabled is null" / "anonymize_client_ip is null"
            if not 3600 * 1000 <= b.get("interval", 0) <= 365 * DAY:
                return self.reply(422)              # validateIvl: an hour to a year
            AG["ql_puts"].append(b)
            AG["ql"] = b
            return self.reply(200)
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
        if self.path == "/control/filtering/set_rules":
            AG["rule_posts"].append(b)
            AG["rules"] = b["rules"]                # the whole list replaces the old one
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


    def octets(self):
        return sorted(int(m.group(1)) for m in map(OURS.match, AG["rules"]) if m)

    def test_ptr_for_172_16_slash_12_is_answered_here_not_by_the_router(self):
        fresh()
        self.run_hook()
        self.assertEqual(self.octets(), list(range(16, 32)), "a PTR in 172.16.0.0/12 still goes to the router and loops")
        self.assertIn("||16.172.in-addr.arpa^$dnstype=PTR,dnsrewrite=NXDOMAIN", AG["rules"])
        self.assertTrue(any(r.startswith("!") and "901" in r for r in AG["rules"]), "no marker line says where the rules come from")
        self.assertEqual(AG["dns"]["local_ptr_upstreams"], ["192.168.178.1"], "the LAN's own names must still come from the router")
        self.run_hook()
        self.assertEqual(len(AG["rule_posts"]), 1, "unchanged rules were written again")

    def test_the_owners_rules_stay_in_their_place(self):
        owner = ["||ads.example^", "@@||bank.example^", "! mine", "||16.172.in-addr.arpa^"]
        fresh(rules=owner)
        self.run_hook()
        self.assertEqual(AG["rules"][:4], owner, "a rule the owner wrote in the UI was changed or moved")
        self.assertEqual(self.octets(), list(range(16, 32)))
        self.run_hook()
        self.assertEqual(len(AG["rule_posts"]), 1)

    def test_old_laib_rules_are_replaced_not_doubled(self):
        fresh(rules=["||x.example^", "! laib (#901): old marker", "||40.172.in-addr.arpa^$dnstype=PTR,dnsrewrite=NXDOMAIN",
                     "||16.172.in-addr.arpa^$dnstype=PTR,dnsrewrite=NXDOMAIN"])
        self.run_hook()
        self.assertEqual(self.octets(), list(range(16, 32)))
        self.assertEqual(sum(r.startswith("! laib") for r in AG["rules"]), 1)
        self.assertEqual(AG["rules"][0], "||x.example^")

    def test_a_lan_inside_172_16_keeps_its_names_from_the_router(self):
        fresh()
        # gateway 172.20.0.1, little-endian hex
        self.routes.write_text(HEADER + "eno1\t00000000\t010014AC\t0003\t0\t0\t100\t00000000\t0\t0\t0\n")
        self.run_hook()
        self.assertEqual(AG["dns"]["local_ptr_upstreams"], ["172.20.0.1"])
        self.assertEqual(self.octets(), [o for o in range(16, 32) if o != 20], "the LAN's own reverse zone was answered with NXDOMAIN")

    def test_the_query_log_keeps_seven_days(self):
        fresh()
        self.run_hook()
        self.assertEqual(AG["ql"]["interval"], 7 * DAY)
        self.assertTrue(AG["ql"]["enabled"])
        self.run_hook()
        self.assertEqual(len(AG["ql_puts"]), 1, "an unchanged query log setting was written again")

    def test_the_query_log_days_are_a_value(self):
        fresh()
        self.run_hook(QUERYLOG_DAYS="30")
        self.assertEqual(AG["ql"]["interval"], 30 * DAY)

    def test_an_impossible_query_log_value_changes_nothing_and_says_so(self):
        fresh()
        p = self.run_hook(QUERYLOG_DAYS="400")
        self.assertEqual(AG["ql_puts"], [])
        self.assertIn("QUERYLOG_DAYS", p.stdout + p.stderr)


if __name__ == "__main__":
    unittest.main()
