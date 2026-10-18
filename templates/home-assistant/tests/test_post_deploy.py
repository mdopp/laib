"""#349: home-assistant's post-deploy mints the Home Assistant tokens itself,
against a fake Home Assistant (its HTTP API and its WebSocket API).

ServiceBay onboarded Home Assistant as OSCAR_HA_ADMIN_USERNAME with a
generated OSCAR_HA_ADMIN_PASSWORD and kept one administrator token in a file
under HA's config folder that every consumer read. Here the hook onboards
(fresh box) or logs in (a box from ServiceBay, the carried password) only
when it has no administrator token that works, mints one token per consumer
and hands each back (`laib-secret:`): Haus and the check get administrator
tokens, the Automat too (HA's automation config API is admin-only), and the
Bote's `ha` tool a token of a separate non-admin user "bote"."""
import base64, hashlib, json, os, struct, subprocess, threading, unittest, urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

HOOK = Path(__file__).resolve().parent.parent / "post-deploy"
ADMIN_PW = "-ha-admin-pw-9q"
BOTE_PW = "-ha-bote-pw-3x"
ADMIN_SECRETS = ("HA_TOKEN", "HAUS_HA_TOKEN", "BOTE_AUTOMAT_HA_TOKEN")
HA = {}


def fresh(onboarded=None):
    """onboarded: None for a fresh Home Assistant, else (username, password) of its owner"""
    HA.clear()
    HA.update(users={}, creds={}, tokens={}, codes={}, flows={}, logins=[], onboarding=set(), core={}, n=0)
    if onboarded:
        uid = add_user("Owner", admin=True, owner=True)
        HA["creds"][onboarded[0]] = (onboarded[1], uid)
        HA["onboarding"] = {"user", "core_config", "analytics", "integration"}


def add_user(name, admin, owner=False):
    HA["n"] += 1
    uid = f"user{HA['n']}"
    HA["users"][uid] = {"id": uid, "name": name, "is_owner": owner, "is_active": True, "local_only": False, "system_generated": False,
                        "group_ids": ["system-admin" if admin else "system-users"]}
    return uid


def token_for(uid, long_lived):
    HA["n"] += 1
    t = f"{'llat' if long_lived else 'access'}-{HA['n']}-{uid}"
    HA["tokens"][t] = uid
    return t


def is_admin(uid):
    return "system-admin" in HA["users"][uid]["group_ids"]


def username(uid):
    return next((u for u, (_, i) in HA["creds"].items() if i == uid), None)


class FakeHA(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def reply(self, code, body=None):
        data = json.dumps(body).encode() if body is not None else b""
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def body(self):
        raw = self.rfile.read(int(self.headers.get("Content-Length") or 0))
        if self.headers.get("Content-Type", "").startswith("application/x-www-form-urlencoded"):
            return dict(urllib.parse.parse_qsl(raw.decode()))
        return json.loads(raw or b"{}")

    def user(self):
        a = self.headers.get("Authorization", "")
        return HA["tokens"].get(a[7:]) if a.startswith("Bearer ") else None

    def do_GET(self):
        if self.path == "/api/websocket":
            return self.websocket()
        if self.path == "/auth/providers":
            if "user" not in HA["onboarding"]:
                return self.reply(400, {"message": "Onboarding not finished", "code": "onboarding_required"})
            return self.reply(200, {"providers": [{"type": "homeassistant", "id": None, "name": "Home Assistant Local"}]})
        if self.path == "/api/onboarding":
            if len(HA["onboarding"]) == 4:
                return self.reply(404)
            return self.reply(200, [{"step": s, "done": s in HA["onboarding"]} for s in ("user", "core_config", "analytics", "integration")])
        uid = self.user()
        if uid is None:
            return self.reply(401)
        if self.path == "/api/":
            return self.reply(200, {"message": "API running."})
        if self.path == "/api/config/config_entries/entry":
            return self.reply(200, [{"domain": "met", "title": "Home"}])   # every user may list them over REST, as in HA
        if self.path == "/api/states/sun.sun":
            return self.reply(200, {"entity_id": "sun.sun", "state": "above_horizon"})
        self.reply(404)

    def do_POST(self):
        b = self.body()
        if self.path == "/api/onboarding/users":
            if "user" in HA["onboarding"]:
                return self.reply(403, {"message": "User step already done"})
            uid = add_user(b["name"], admin=True, owner=True)
            HA["creds"][b["username"]] = (b["password"], uid)
            HA["onboarding"].add("user")
            HA["codes"]["code-onb"] = uid
            return self.reply(200, {"auth_code": "code-onb"})
        if self.path == "/auth/login_flow":
            HA["flows"]["flow1"] = b["client_id"]
            return self.reply(200, {"type": "form", "flow_id": "flow1", "step_id": "init"})
        if self.path.startswith("/auth/login_flow/"):
            HA["logins"].append(b["username"])
            pw, uid = HA["creds"].get(b["username"], (None, None))
            if pw is None or pw != b["password"]:
                return self.reply(200, {"type": "form", "flow_id": "flow1", "errors": {"base": "invalid_auth"}})
            HA["codes"]["code-" + uid] = uid
            return self.reply(200, {"type": "create_entry", "result": "code-" + uid})
        if self.path == "/auth/token":
            uid = HA["codes"].pop(b.get("code"), None)
            if b.get("grant_type") != "authorization_code" or uid is None:
                return self.reply(400, {"error": "invalid_request"})
            return self.reply(200, {"access_token": token_for(uid, False), "token_type": "Bearer", "refresh_token": "r", "expires_in": 1800})
        uid = self.user()
        if self.path.startswith("/api/onboarding/"):
            step = self.path.rsplit("/", 1)[1]
            if uid is None or step in HA["onboarding"] or (step == "integration" and not b.get("redirect_uri")):
                return self.reply(403 if uid else 401)
            HA["onboarding"].add(step)
            return self.reply(200, {})
        self.reply(404)

    # --- the WebSocket API, as Home Assistant speaks it ---------------------
    def websocket(self):
        key = self.headers["Sec-WebSocket-Key"]
        accept = base64.b64encode(hashlib.sha1((key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").encode()).digest()).decode()
        self.send_response(101)
        self.send_header("Upgrade", "websocket")
        self.send_header("Connection", "Upgrade")
        self.send_header("Sec-WebSocket-Accept", accept)
        self.end_headers()
        self.wfile.flush()
        self.ws_send({"type": "auth_required", "ha_version": "2026.10.2"})
        msg = self.ws_recv()
        uid = HA["tokens"].get(msg.get("access_token"))
        if msg.get("type") != "auth" or uid is None:
            self.ws_send({"type": "auth_invalid", "message": "Invalid access token or password"})
            return
        self.ws_send({"type": "auth_ok", "ha_version": "2026.10.2"})
        self.ws_send({"type": "event", "event": {"noise": "an event between results"}})   # the client reads past it
        while True:
            try:
                msg = self.ws_recv()
            except (ConnectionError, ValueError, struct.error):  # the client hung up
                return
            if msg is None:
                return
            self.ws_send(self.ws_command(uid, msg))

    def ws_command(self, uid, m):
        t, i = m["type"], m["id"]
        ok = lambda r=None: {"id": i, "type": "result", "success": True, "result": r}
        err = lambda code, text: {"id": i, "type": "result", "success": False, "error": {"code": code, "message": text}}
        if t == "auth/long_lived_access_token":
            return ok(token_for(uid, True))
        if not is_admin(uid):
            return err("unauthorized", "Unauthorized")
        if t == "config/auth/list":
            return ok([dict(u, username=username(u["id"])) for u in HA["users"].values()])
        if t == "config/auth/create":
            nid = add_user(m["name"], admin="system-admin" in (m.get("group_ids") or []))
            return ok({"user": dict(HA["users"][nid], username=None)})
        if t == "config/auth/update":
            HA["users"][m["user_id"]]["group_ids"] = m["group_ids"]
            return ok({"user": HA["users"][m["user_id"]]})
        if t == "config/auth_provider/homeassistant/create":
            if username(m["user_id"]):
                return err("credentials_exist", "User already has credentials for this provider")
            HA["creds"][m["username"]] = (m["password"], m["user_id"])
            return ok()
        if t == "config/auth_provider/homeassistant/admin_change_password":
            if not HA["users"][uid]["is_owner"]:
                return err("unauthorized", "Unauthorized")
            name = username(m["user_id"])
            HA["creds"][name] = (m["password"], m["user_id"])
            return ok()
        if t == "config/core/update":
            HA["core"].update({k: v for k, v in m.items() if k not in ("id", "type")})
            return ok()
        return err("unknown_command", t)

    def ws_send(self, obj):
        data = json.dumps(obj).encode()
        n = len(data)
        head = bytes([0x81, n]) if n < 126 else bytes([0x81, 126]) + struct.pack(">H", n) if n < 65536 else bytes([0x81, 127]) + struct.pack(">Q", n)
        self.wfile.write(head + data)
        self.wfile.flush()

    def ws_recv(self):
        b0, b1 = self.rfile.read(2)
        if b0 & 0x0F == 8:
            return None
        if not b1 & 0x80:
            raise AssertionError("a client frame must be masked (RFC 6455 5.1)")
        n = b1 & 0x7F
        if n == 126:
            n = struct.unpack(">H", self.rfile.read(2))[0]
        elif n == 127:
            n = struct.unpack(">Q", self.rfile.read(8))[0]
        mask = self.rfile.read(4)
        data = bytes(c ^ mask[k % 4] for k, c in enumerate(self.rfile.read(n)))
        return json.loads(data)


class PostDeploy(unittest.TestCase):
    def setUp(self):
        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), FakeHA)
        self.srv.daemon_threads = True
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.addCleanup(self.srv.shutdown)

    def run_hook(self, tokens=None, user="laib", password=ADMIN_PW, bote_password=BOTE_PW):
        env = {"PATH": os.environ["PATH"], "LAIB_DOMAIN": "box.test", "LAIB_TZ": "Europe/Berlin",
               "HA_URL": f"http://127.0.0.1:{self.srv.server_port}", "HA_ADMIN_USER": user,
               "LAIB_SECRET_HA_ADMIN_PASSWORD": password, "LAIB_SECRET_HA_BOTE_PASSWORD": bote_password}
        for name, value in (tokens or {}).items():
            env["LAIB_SECRET_" + name] = value
        p = subprocess.run([str(HOOK)], env=env, capture_output=True, text=True, timeout=60)
        handed = dict(l[len("laib-secret: "):].split("=", 1) for l in p.stdout.splitlines() if l.startswith("laib-secret: "))
        rest = "\n".join(l for l in (p.stdout + "\n" + p.stderr).splitlines() if not l.startswith("laib-secret: "))
        for value in list(handed.values()) + list((tokens or {}).values()) + [password, bote_password]:
            if value:
                self.assertNotIn(value, rest, "a token or a password in the hook's log (#214)")
        return p, handed, rest

    def kind(self, token):
        uid = HA["tokens"].get(token)
        return None if uid is None else ("admin" if is_admin(uid) else "user")

    def test_a_fresh_home_assistant_is_onboarded_and_every_token_minted(self):
        fresh()
        p, handed, log = self.run_hook()
        self.assertEqual(p.returncode, 0, log)
        self.assertEqual(HA["onboarding"], {"user", "core_config", "analytics", "integration"}, "onboarding left half done")
        self.assertEqual(HA["creds"]["laib"][0], ADMIN_PW)
        self.assertEqual(HA["core"].get("time_zone"), "Europe/Berlin")
        self.assertEqual(set(handed), set(ADMIN_SECRETS) | {"BOTE_HA_TOKEN"})
        for name in ADMIN_SECRETS:
            self.assertEqual(self.kind(handed[name]), "admin", name)
            self.assertTrue(handed[name].startswith("llat-"), f"{name} is a short-lived session, not a long-lived token")
        self.assertEqual(len({handed[n] for n in handed}), 4, "one token per consumer, each revocable on its own")
        bote = HA["tokens"][handed["BOTE_HA_TOKEN"]]
        self.assertEqual(self.kind(handed["BOTE_HA_TOKEN"]), "user", "the Bote's token must not be an administrator's")
        self.assertEqual((username(bote), HA["users"][bote]["group_ids"]), ("bote", ["system-users"]))
        self.assertEqual(HA["creds"]["bote"][0], BOTE_PW)

    def test_working_tokens_are_kept_and_nobody_logs_in(self):
        fresh()
        _, handed, _ = self.run_hook()
        HA["logins"].clear()
        p, again, log = self.run_hook(tokens=handed)
        self.assertEqual(p.returncode, 0, log)
        self.assertEqual(again, {}, "a working token was replaced")
        self.assertEqual(HA["logins"], [], "logged in although every token works")

    def test_a_revoked_token_is_replaced_with_the_admin_token_that_still_works(self):
        fresh()
        _, handed, _ = self.run_hook()
        del HA["tokens"][handed["HAUS_HA_TOKEN"]], HA["tokens"][handed["BOTE_HA_TOKEN"]]
        HA["logins"].clear()
        p, again, log = self.run_hook(tokens=handed, password="")
        self.assertEqual(p.returncode, 0, log)
        self.assertEqual(set(again), {"HAUS_HA_TOKEN", "BOTE_HA_TOKEN"})
        self.assertEqual(self.kind(again["HAUS_HA_TOKEN"]), "admin")
        self.assertEqual(self.kind(again["BOTE_HA_TOKEN"]), "user")
        self.assertEqual(HA["logins"], ["bote"], "the administrator logged in although HA_TOKEN works")

    def test_a_box_from_servicebay_logs_in_as_its_admin_and_the_bote_stays_no_admin(self):
        # ServiceBay onboarded as oscar; the owner once pasted an administrator's token as BOTE_HA_TOKEN,
        # and a "bote" user exists that someone made an administrator
        fresh(onboarded=("oscar", ADMIN_PW))
        owner = next(iter(HA["users"]))
        pasted = token_for(owner, True)
        bote = add_user("Bote", admin=True)
        HA["creds"]["bote"] = ("an old password", bote)
        p, handed, log = self.run_hook(tokens={"BOTE_HA_TOKEN": pasted}, user="oscar")
        self.assertEqual(p.returncode, 0, log)
        self.assertEqual(HA["logins"][0], "oscar")
        self.assertEqual(set(handed), set(ADMIN_SECRETS) | {"BOTE_HA_TOKEN"}, "the pasted admin token stayed the Bote's")
        self.assertEqual(HA["tokens"][handed["BOTE_HA_TOKEN"]], bote, "a second Bote user instead of the one there")
        self.assertEqual(HA["users"][bote]["group_ids"], ["system-users"])
        self.assertEqual(self.kind(handed["BOTE_HA_TOKEN"]), "user")
        self.assertIn(pasted, HA["tokens"], "the owner's own token was revoked: not Laib's to take")

    def test_no_admin_login_and_no_token_is_red_and_says_what_to_set(self):
        fresh(onboarded=("oscar", "the real one"))
        p, handed, log = self.run_hook(user="oscar")
        self.assertNotEqual(p.returncode, 0)
        self.assertEqual(handed, {})
        self.assertIn("laib secret set HA_ADMIN_PASSWORD", log)
        self.assertIn("HA_ADMIN_USER", log)


if __name__ == "__main__":
    unittest.main()
