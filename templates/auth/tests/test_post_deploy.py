"""auth's post-deploy seeds LLDAP through its GraphQL API (127.0.0.1:17170)
and sets the passwords LLDAP's GraphQL cannot set.

#214: no password on any process's argv. `ps` and /proc/<pid>/cmdline show
every argv on the box to every local user. A `laib-exec:` line becomes
`podman exec <container> <words…>` (internal/apply/apply.go), and the
command it runs inside the container shows on the host as well. So the
passwords travel from the hook's own process to LLDAP's socket. That
socket here is a fake LDAP server on 127.0.0.1:3890, which records what
arrives.

#209: generated secrets are base64url, so one in 64 starts with "-"; such a
password is still set as it is."""
import json, os, socketserver, subprocess, sys, threading, unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

HOOK = Path(__file__).resolve().parent.parent / "post-deploy"
STATE = {"users": [], "groups": [], "adds": []}
BASE_DN = "dc=vm,dc=test"
SECRETS = {"LLDAP_ADMIN_PASSWORD": "-adm1n_x", "OPERATOR_PASSWORD": "-6op_pw", "RADICALE_LDAP_PASSWORD": "--rad-pw", "FILE_SHARE_LDAP_PASSWORD": "-fs_pw",
           "JELLYFIN_LDAP_PASSWORD": "-jf-ro-pw", "HAUS_RADICALE_PASSWORD": "-haus-cal-pw",
           "KALENDER_RADICALE_PASSWORD": "-kal-pw", "HA_RADICALE_PASSWORD": "-ha-cal-pw"}   # #256: Jellyfin's LDAP plugin binds read-only, like radicale
ADMIN_DN = "uid=admin,ou=people," + BASE_DN
LDAP = {"pw": {}, "set": [], "binds": [], "modify": "take"}   # dn → password; dns whose password was set; (dn, ok) per bind;
# "take", "swallow" (success, nothing stored) or "refuse" (insufficientAccessRights)


class FakeLLDAP(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])) or b"{}")
        if self.path == "/auth/simple/login":
            out = {"token": "t"}
        else:
            q = body["query"]
            if q.startswith("{ groups"):
                out = {"data": {"groups": [{"id": i, "displayName": g} for i, g in enumerate(STATE["groups"])]}}
            elif "createGroup" in q:
                STATE["groups"].append(body["variables"]["n"])
                out = {"data": {"createGroup": {"id": len(STATE["groups"]) - 1}}}
            elif q.startswith("{ users"):
                out = {"data": {"users": [{"id": u} for u in STATE["users"]]}}
            elif "createUser" in q:
                STATE["users"].append(body["variables"]["u"]["id"])
                out = {"data": {"createUser": {"id": body["variables"]["u"]["id"]}}}
            elif "user(userId" in q:
                out = {"data": {"user": {"groups": []}}}
            else:
                STATE["adds"].append(body.get("variables", {}).get("u"))
                out = {"data": {"addUserToGroup": {"ok": True}}}
        data = json.dumps(out).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)


def tlv(b, i):
    """one BER element at b[i:]: its tag, its content, the index after it"""
    tag, n, i = b[i], b[i + 1], i + 2
    if n & 0x80:
        k = n & 0x7F
        n, i = int.from_bytes(b[i:i + k], "big"), i + k
    return tag, b[i:i + n], i + n


def ber(tag, body):
    n = len(body)
    k = (n.bit_length() + 7) // 8
    return bytes([tag]) + (bytes([n]) if n < 128 else bytes([0x80 | k]) + n.to_bytes(k, "big")) + body


class FakeLDAP(socketserver.BaseRequestHandler):
    """simple bind and the RFC 3062 password modify, as LLDAP answers them"""
    def handle(self):
        f, who = self.request.makefile("rb"), None
        while True:
            head = f.read(2)
            if len(head) < 2:
                return
            n = head[1]
            if n & 0x80:
                n = int.from_bytes(f.read(n & 0x7F), "big")
            msg = f.read(n)
            _, mid, i = tlv(msg, 0)
            op, val, _ = tlv(msg, i)
            if op == 0x60:   # BindRequest: version, name, simple
                _, _, j = tlv(val, 0)
                _, dn, j = tlv(val, j)
                _, pw, _ = tlv(val, j)
                ok = LDAP["pw"].get(dn.decode()) == pw.decode()
                who = dn.decode() if ok else None
                LDAP["binds"].append((dn.decode(), ok))
                code, rop = (0 if ok else 49), 0x61
            elif op == 0x77:  # ExtendedRequest: requestName [0], requestValue [1]
                _, name, j = tlv(val, 0)
                _, value, _ = tlv(val, j)
                _, seq, _ = tlv(value, 0)
                fields, k = {}, 0
                while k < len(seq):
                    t, v, k = tlv(seq, k)
                    fields[t] = v.decode()
                code, rop = 50, 0x78   # insufficientAccessRights
                if LDAP["modify"] == "refuse":
                    pass
                elif name == b"1.3.6.1.4.1.4203.1.11.1" and who == ADMIN_DN and 0x80 in fields and 0x82 in fields:
                    code = 0
                    if LDAP["modify"] == "take":
                        LDAP["pw"][fields[0x80]] = fields[0x82]
                        LDAP["set"].append(fields[0x80])
            else:             # UnbindRequest, or anything else: the connection ends
                return
            self.request.sendall(ber(0x30, ber(0x02, mid) + ber(rop, ber(0x0A, bytes([code])) + ber(0x04, b"") + ber(0x04, b""))))


class PostDeploy(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        try:
            cls.srv = HTTPServer(("127.0.0.1", 17170), FakeLLDAP)
        except OSError:
            raise unittest.SkipTest("127.0.0.1:17170 is taken")
        try:
            socketserver.ThreadingTCPServer.allow_reuse_address = True
            cls.ldap = socketserver.ThreadingTCPServer(("127.0.0.1", 3890), FakeLDAP)
        except OSError:
            cls.srv.server_close()
            raise unittest.SkipTest("127.0.0.1:3890 is taken")
        for s in (cls.srv, cls.ldap):
            threading.Thread(target=s.serve_forever, daemon=True).start()

    @classmethod
    def tearDownClass(cls):
        for s in (cls.srv, cls.ldap):
            s.shutdown()
            s.server_close()

    def run_hook(self, users, groups, ok=True, modify="take", op="core", **secrets):
        secrets = dict(SECRETS, **secrets)
        STATE["users"], STATE["groups"], STATE["adds"] = users, groups, []
        LDAP["pw"], LDAP["set"], LDAP["binds"], LDAP["modify"] = {ADMIN_DN: secrets["LLDAP_ADMIN_PASSWORD"]}, [], [], modify
        env = dict(os.environ, LAIB_DOMAIN="vm.test", BASE_DN=BASE_DN, OPERATOR_USER=op, OPERATOR_EMAIL="core@vm.test",
                   **{"LAIB_SECRET_" + k: v for k, v in secrets.items()})
        p = subprocess.run([sys.executable, str(HOOK)], env=env, capture_output=True, text=True, timeout=60)
        if not ok:
            self.assertNotEqual(p.returncode, 0, p.stdout + p.stderr)
            for name, s in secrets.items():
                self.assertFalse(s and s in p.stdout + p.stderr, f"a red hook shows {name}: {p.stdout + p.stderr}")
            return p.stderr
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        # what laib runs for the hook: `podman exec <container> <words…>` per laib-exec line
        argvs = [["podman", "exec"] + l[len("laib-exec: "):].split() for l in p.stdout.splitlines() if l.startswith("laib-exec: ")]
        for argv in argvs:
            for name, s in SECRETS.items():
                self.assertFalse(any(s in w for w in argv), f"{name} is on a process's argv, where ps shows it: {argv}")
        return p.stdout

    def test_passwords_reach_lldap_and_no_argv(self):
        self.run_hook([], [])
        self.assertEqual(LDAP["pw"].get("uid=core,ou=people," + BASE_DN), SECRETS["OPERATOR_PASSWORD"], LDAP)
        self.assertEqual(LDAP["pw"].get("uid=radicale,ou=people," + BASE_DN), SECRETS["RADICALE_LDAP_PASSWORD"], LDAP)
        # file-share's reader (#296): who is family, read-only like radicale's
        self.assertEqual(LDAP["pw"].get("uid=file-share,ou=people," + BASE_DN), SECRETS["FILE_SHARE_LDAP_PASSWORD"], LDAP)
        self.assertIn("file-share", STATE["users"])
        self.assertEqual(LDAP["pw"].get("uid=jellyfin,ou=people," + BASE_DN), SECRETS["JELLYFIN_LDAP_PASSWORD"], LDAP)
        self.assertIn("jellyfin", STATE["users"])
        # and each account binds with it afterwards: a set that did not take is red, not "seeded"
        for uid in ("core", "radicale", "file-share", "jellyfin"):
            self.assertIn(("uid=%s,ou=people,%s" % (uid, BASE_DN), True), LDAP["binds"], LDAP["binds"])

    def test_haus_gets_its_radicale_account(self):
        # laib#418: Haus reads and writes the household calendar as its own
        # LLDAP account, seeded here like radicale's reader, but no LDAP reader
        self.run_hook([], [])
        self.assertIn("haus", STATE["users"])
        self.assertEqual(LDAP["pw"].get("uid=haus,ou=people," + BASE_DN), SECRETS["HAUS_RADICALE_PASSWORD"], LDAP)
        self.assertIn(("uid=haus,ou=people," + BASE_DN, True), LDAP["binds"])
        self.assertNotIn("haus", STATE["adds"], "Haus's calendar account must not read the directory (lldap_strict_readonly) or join a group")
        # set again on every run, as a service secret (#209)
        self.run_hook(["core", "haus"], ["admins", "family", "lldap_strict_readonly"])
        self.assertIn("uid=haus,ou=people," + BASE_DN, LDAP["set"], LDAP)

    def test_the_calendar_mirror_and_home_assistant_get_their_radicale_accounts(self):
        # laib#495: the mirror writes as `kalender`, Home Assistant reads as `homeassistant`: nobody's password
        self.run_hook([], [])
        for uid, secret in (("kalender", "KALENDER_RADICALE_PASSWORD"), ("homeassistant", "HA_RADICALE_PASSWORD")):
            self.assertIn(uid, STATE["users"])
            self.assertEqual(LDAP["pw"].get(f"uid={uid},ou=people," + BASE_DN), SECRETS[secret], LDAP)
            self.assertIn((f"uid={uid},ou=people," + BASE_DN, True), LDAP["binds"])
            self.assertNotIn(uid, STATE["adds"], f"{uid} must not read the directory or join a group")

    def test_radicales_password_is_set_again_when_the_account_exists(self):
        # a set that failed once (the account exists, the password does not) is not final:
        # radicale's password is a service secret, so every run sets it again
        self.run_hook(["core", "radicale", "jellyfin"], ["admins", "family", "lldap_strict_readonly"])
        self.assertIn("uid=radicale,ou=people," + BASE_DN, LDAP["set"], LDAP)
        self.assertIn("uid=jellyfin,ou=people," + BASE_DN, LDAP["set"], LDAP)
        self.assertNotIn("uid=core,ou=people," + BASE_DN, LDAP["set"],
                         "the operator's own password must not be reset once the account exists")


    def test_a_set_lldap_refuses_is_red(self):
        err = self.run_hook([], [], ok=False, modify="refuse")
        self.assertIn("core's password: set: result 50", err)

    def test_a_set_that_did_not_take_is_red(self):
        # LLDAP says success and stores nothing: only the bind with the new password can tell
        err = self.run_hook([], [], ok=False, modify="swallow")
        self.assertIn("core's password: bind with it: result 49", err)

    def test_a_long_password_with_any_characters_arrives_as_it_is(self):
        # over 255 bytes, so every BER length on the way is the long form; quotes,
        # backslashes, a leading "-" and non-ASCII are just bytes to LDAP
        pw = '-"\\ä€ \'' + "x" * 300
        self.run_hook([], [], RADICALE_LDAP_PASSWORD=pw)
        self.assertEqual(LDAP["pw"].get("uid=radicale,ou=people," + BASE_DN), pw, LDAP)
        self.assertIn(("uid=radicale,ou=people," + BASE_DN, True), LDAP["binds"])

    def test_an_empty_password_or_a_name_ldap_cannot_address_is_red_before_anything_is_created(self):
        # an empty password binds anonymously, which proves nothing; "a,ou=x" is another entry's DN
        for kw in ({"RADICALE_LDAP_PASSWORD": ""}, {"FILE_SHARE_LDAP_PASSWORD": ""}, {"JELLYFIN_LDAP_PASSWORD": ""}, {"OPERATOR_PASSWORD": ""}, {"op": "core,ou=admins"}, {"op": "a+b"}, {"op": "a\\,b"}):
            with self.subTest(**kw):
                self.run_hook([], [], ok=False, **kw)
                self.assertEqual((STATE["users"], LDAP["set"]), ([], []), "nothing may be created or set first")


if __name__ == "__main__":
    unittest.main()
