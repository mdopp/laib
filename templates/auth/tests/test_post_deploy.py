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
STATE = {"users": [], "groups": [], "adds": [], "members": {}, "deleted": []}
BASE_DN = "dc=vm,dc=test"
SECRETS = {"LLDAP_ADMIN_PASSWORD": "-adm1n_x", "OPERATOR_PASSWORD": "-6op_pw", "RADICALE_LDAP_PASSWORD": "--rad-pw", "FILE_SHARE_LDAP_PASSWORD": "-fs_pw",
           "JELLYFIN_LDAP_PASSWORD": "-jf-ro-pw", "HOMES_RADICALE_PASSWORD": "-haus-cal-pw",
           "KALENDER_RADICALE_PASSWORD": "-kal-pw", "HA_RADICALE_PASSWORD": "-ha-cal-pw", "AUDIOBOOKSHELF_LDAP_PASSWORD": "-abs-ro-pw"}   # #256: Jellyfin's LDAP plugin binds read-only, like radicale
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
            elif "group(groupId" in q:
                out = {"data": {"group": {"users": [{"id": u} for u in STATE["members"].get(body["variables"]["g"], [])]}}}
            elif "deleteUser" in q:
                u = body["variables"]["u"]
                STATE["users"].remove(u)
                STATE["deleted"].append(u)
                for m in STATE["members"].values():
                    m.discard(u)
                out = {"data": {"deleteUser": {"ok": True}}}
            else:
                v = body.get("variables", {})
                STATE["adds"].append(v.get("u"))
                STATE["members"].setdefault(v.get("g"), set()).add(v.get("u"))
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

    def run_hook(self, users, groups, ok=True, modify="take", op="core", extra=None, admin=None, **secrets):
        secrets = dict(SECRETS, **secrets)
        STATE["users"], STATE["groups"], STATE["adds"], STATE["deleted"] = users, groups, [], []
        LDAP["pw"], LDAP["set"], LDAP["binds"], LDAP["modify"] = {ADMIN_DN: admin or secrets["LLDAP_ADMIN_PASSWORD"]}, [], [], modify
        env = dict(os.environ, LAIB_DOMAIN="vm.test", BASE_DN=BASE_DN, OPERATOR_USER=op, OPERATOR_EMAIL="core@vm.test",
                   **{"LAIB_SECRET_" + k: v for k, v in secrets.items()}, **(extra or {}))
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
        # audiobookshelf's reader: who is in admins, so they are ABS admins too
        self.assertEqual(LDAP["pw"].get("uid=audiobookshelf,ou=people," + BASE_DN), SECRETS["AUDIOBOOKSHELF_LDAP_PASSWORD"], LDAP)
        self.assertIn("audiobookshelf", STATE["users"])
        # and each account binds with it afterwards: a set that did not take is red, not "seeded"
        for uid in ("core", "radicale", "file-share", "jellyfin", "audiobookshelf"):
            self.assertIn(("uid=%s,ou=people,%s" % (uid, BASE_DN), True), LDAP["binds"], LDAP["binds"])

    def test_a_rotation_changes_the_admin_password_in_lldap_first(self):
        # #665: LLDAP_ADMIN_PASSWORD, carried from ServiceBay, rotated: the admin sets it for itself
        # over LDAP, then hands it back, and the rest of the run already binds with it
        new = "-adm1n-rotated-665"
        out = self.run_hook([], [], extra={"LAIB_ROTATE_LLDAP_ADMIN_PASSWORD": new})
        self.assertEqual(LDAP["pw"][ADMIN_DN], new, LDAP)
        self.assertIn("laib-secret: LLDAP_ADMIN_PASSWORD=" + new, out.splitlines())
        self.assertNotIn(new, "\n".join(l for l in out.splitlines() if not l.startswith("laib-secret: ")))
        self.assertEqual(LDAP["pw"].get("uid=radicale,ou=people," + BASE_DN), SECRETS["RADICALE_LDAP_PASSWORD"], "seeding stopped after the rotation")
        # a run before took it already (the session died before laib stored it): only handed back
        out = self.run_hook([], [], admin=new, extra={"LAIB_ROTATE_LLDAP_ADMIN_PASSWORD": new})
        self.assertIn("laib-secret: LLDAP_ADMIN_PASSWORD=" + new, out.splitlines())

    def test_a_rotation_lldap_refuses_keeps_the_old_one(self):
        err = self.run_hook([], [], ok=False, modify="refuse", extra={"LAIB_ROTATE_LLDAP_ADMIN_PASSWORD": "-adm1n-rotated-665"})
        self.assertIn("NOT rotated", err)
        self.assertEqual(LDAP["pw"][ADMIN_DN], SECRETS["LLDAP_ADMIN_PASSWORD"])

    def test_homes_gets_its_radicale_account(self):
        # laib#418: Homes reads and writes the household calendar as its own
        # LLDAP account, seeded here like radicale's reader, but no LDAP reader
        self.run_hook([], [])
        self.assertIn("homes", STATE["users"])
        self.assertEqual(LDAP["pw"].get("uid=homes,ou=people," + BASE_DN), SECRETS["HOMES_RADICALE_PASSWORD"], LDAP)
        self.assertIn(("uid=homes,ou=people," + BASE_DN, True), LDAP["binds"])
        self.assertNotIn("homes", STATE["adds"], "Homes's calendar account must not read the directory (lldap_strict_readonly) or join a group")
        self.assertEqual(STATE["deleted"], [], "nothing to remove on a fresh box")
        # set again on every run, as a service secret (#209)
        self.run_hook(["core", "homes"], ["admins", "family", "lldap_strict_readonly"])
        self.assertIn("uid=homes,ou=people," + BASE_DN, LDAP["set"], LDAP)

    def test_the_old_calendar_account_haus_goes_once_homes_is_seeded(self):
        # laib#1021, ADR 0031: the account was `haus`; the rename seeds `homes` with the same
        # password (HOMES_RADICALE_PASSWORD was HAUS_RADICALE_PASSWORD) and removes `haus`
        self.run_hook(["core", "haus"], ["admins", "family", "lldap_strict_readonly"])
        self.assertIn(("uid=homes,ou=people," + BASE_DN, True), LDAP["binds"])
        self.assertEqual(STATE["deleted"], ["haus"])
        self.assertNotIn("haus", STATE["users"])

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

    def accounts(self, tmp, declared, users, groups, members=None, ok=True, **secrets):
        """a run with identity/accounts.json in the render folder (laib#1009); members: group name → users"""
        os.makedirs(os.path.join(tmp, "identity"), exist_ok=True)
        with open(os.path.join(tmp, "identity", "accounts.json"), "w") as f:
            json.dump([{"user": u, "secret": n, "service": u} for u, n in declared], f)
        STATE["members"] = {groups.index(g): set(m) for g, m in (members or {}).items()}
        return self.run_hook(users, groups, ok=ok, extra={"LAIB_RENDER_DIR": tmp}, **secrets)

    def test_a_service_account_is_seeded_with_its_password_and_removed_with_its_service(self):   # laib#1009
        import tempfile
        tmp = tempfile.mkdtemp()
        pw = "-archiv-anna-rad-pw"
        self.accounts(tmp, [("archiv-anna", "ARCHIV_RADICALE_PASSWORD_ANNA")], [], [], ARCHIV_RADICALE_PASSWORD_ANNA=pw)
        dn = "uid=archiv-anna,ou=people," + BASE_DN
        self.assertIn("archiv-anna", STATE["users"])
        self.assertEqual(LDAP["pw"].get(dn), pw, LDAP)
        self.assertIn((dn, True), LDAP["binds"], "a set that did not take is red, not seeded")
        managed = STATE["groups"].index("laib_accounts")
        self.assertEqual(STATE["members"][managed], {"archiv-anna"})
        for g in ("admins", "family", "lldap_strict_readonly"):
            self.assertNotIn("archiv-anna", STATE["members"].get(STATE["groups"].index(g), set()), f"a service account in {g}")
        # the next run sets it again; the instance removed: the account goes, nobody else's
        users = ["core", "homes", "kalender", "archiv-anna", "archiv-ben"]
        self.accounts(tmp, [("archiv-anna", "ARCHIV_RADICALE_PASSWORD_ANNA")], users, ["admins", "family", "lldap_strict_readonly", "laib_accounts"],
                      {"laib_accounts": ["archiv-anna", "archiv-ben"]}, ARCHIV_RADICALE_PASSWORD_ANNA=pw)
        self.assertIn(dn, LDAP["set"])
        self.assertEqual(STATE["deleted"], ["archiv-ben"])
        self.accounts(tmp, [], list(users), ["admins", "family", "lldap_strict_readonly", "laib_accounts"], {"laib_accounts": ["archiv-anna"]})
        self.assertEqual(STATE["deleted"], ["archiv-anna"], "the last one goes too")
        self.assertIn("homes", STATE["users"])

    def test_without_the_list_no_service_account_is_touched(self):   # a Laib before laib#1009
        self.run_hook(["core", "archiv-anna"], ["admins", "family", "lldap_strict_readonly", "laib_accounts"], extra={"LAIB_RENDER_DIR": "/nonexistent"})
        self.assertEqual(STATE["deleted"], [])
        self.assertIn("archiv-anna", STATE["users"])

    def test_a_persons_account_or_one_of_the_hooks_own_is_never_taken_over(self):   # laib#1009
        import tempfile
        tmp = tempfile.mkdtemp()
        # archiv-anna exists and is no service account Laib made: its password stays, the run is red
        err = self.accounts(tmp, [("archiv-anna", "P")], ["core", "archiv-anna"], ["admins", "family", "lldap_strict_readonly"], ok=False, P="-p-1009")
        self.assertIn("not taken over", err)
        self.assertNotIn("uid=archiv-anna,ou=people," + BASE_DN, LDAP["set"])
        # a name the hook keeps itself, one LDAP cannot address, an empty password: red before anything is created
        for user, pw in (("homes", "-p-1009"), ("haus", "-p-1009"), ("core", "-p-1009"), ("a,ou=x", "-p-1009"), ("archiv-anna", "")):
            with self.subTest(user=user):
                self.accounts(tmp, [(user, "P")], [], [], ok=False, P=pw)
                self.assertEqual((STATE["users"], LDAP["set"]), ([], []), "nothing may be created or set first")


if __name__ == "__main__":
    unittest.main()
