"""#526: music-assistant's post-deploy makes Music Assistant usable after
`laib add`, against a fake Music Assistant 2.10 (POST /setup, /auth/login,
the JSON-RPC /api and its provider setup flows) and a fake Jellyfin.

Before, the template only started the container: MA stood on SETUP REQUIRED,
the owner had to make an MA account, connect Jellyfin with a Jellyfin login
and add the integration in Home Assistant by hand. ServiceBay had no Music
Assistant template; its rule (LLDAP/Authelia logins, service accounts minted
by the box) is the one kept here."""
import json, os, subprocess, threading, unittest, urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

HOOK = Path(__file__).resolve().parent.parent / "post-deploy"
MA_PW, JF_ADMIN_PW, JF_MA_PW = "-ma-admin-pw-4R", "-jf-admin-pw-2W", "-jf-ma-pw-6T"
MA, JF = {}, {}


def fresh(onboarded=None, jellyfin=True, music=True):
    MA.clear()
    MA.update(users={}, tokens={}, n=0, providers={}, flows={}, onboard=False, core={}, logins=0, setups=0, logouts=0, fail_jellyfin=False)
    if onboarded:
        MA["users"][onboarded[0]] = onboarded[1]
    JF.clear()
    JF.update(up=jellyfin, users={"admin": {"Id": "u-admin", "Name": "admin", "pw": JF_ADMIN_PW, "Policy": {"IsAdministrator": True}}},
              folders=[{"Name": "Musik", "ItemId": "f-music", "CollectionType": "music"}] if music else [], admin_logins=0)


def new_token(user, name, long_lived=False):
    MA["n"] += 1
    t = f"ma-{'ll' if long_lived else 's'}-{MA['n']}"
    MA["tokens"][t] = {"user": user, "name": name, "id": f"tid{MA['n']}", "long": long_lived}
    return t


class Base(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def reply(self, code, body=None):
        data = (body if isinstance(body, bytes) else json.dumps(body).encode()) if body is not None else b""
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def body(self):
        return json.loads(self.rfile.read(int(self.headers.get("Content-Length") or 0)) or b"{}")


class FakeMA(Base):
    """the parts of Music Assistant 2.10.7 the hook speaks (controllers/webserver, controllers/config)"""

    def do_GET(self):
        if self.path == "/info":
            return self.reply(200, {"server_id": "srv1", "schema_version": 28, "onboard_done": MA["onboard"], "base_url": "http://x:8095"})
        self.reply(404)

    def do_POST(self):
        b = self.body()
        if self.path == "/setup":
            if MA["users"]:
                return self.reply(400, {"success": False, "error": "Setup already completed"})
            MA["setups"] += 1
            MA["users"][b["username"]] = b["password"]
            return self.reply(200, {"success": True, "token": new_token(b["username"], "setup"), "user": {}})
        if self.path == "/auth/login":
            if not MA["users"]:
                return self.reply(403, {"success": False, "error": "Setup required"})
            MA["logins"] += 1
            c = b.get("credentials") or {}
            if b.get("provider_id") != "builtin" or MA["users"].get(c.get("username")) != c.get("password"):
                return self.reply(401, {"success": False, "error": "Invalid username or password"})
            return self.reply(200, {"success": True, "token": new_token(c["username"], b.get("device_name")), "user": {}})
        if self.path == "/auth/logout":
            t = (self.headers.get("Authorization") or "")[7:]
            MA["tokens"].pop(t, None)
            MA["logouts"] += 1
            return self.reply(200, {"success": True})
        if self.path == "/api":
            if not MA["users"]:
                return self.reply(503, b"Setup required")
            tok = MA["tokens"].get((self.headers.get("Authorization") or "")[7:])
            if tok is None:
                return self.reply(401, b"Authentication required")
            return self.rpc(tok, b["command"], b.get("args") or {})
        self.reply(404)

    def step(self, flow, t, **kw):
        return dict({"flow_id": flow, "type": t, "step_id": "user", "entries": [], "errors": {}}, **kw)

    def rpc(self, tok, c, a):
        ok = lambda r: self.reply(200, r)
        if c == "auth/me":
            return ok({"username": tok["user"]})
        if c == "config/core/save":
            MA["core"].setdefault(a["domain"], {}).update(a["values"])
            return ok({})
        if c == "config/providers":
            return ok([dict(p, setup_data=None) for p in MA["providers"].values() if p["domain"] == a.get("provider_domain", p["domain"])])
        if c == "config/providers/setup":
            d = a["provider_domain"]
            if d == "radiobrowser":
                if any(p["domain"] == d for p in MA["providers"].values()):
                    return ok(self.step("f0", "abort", reason="already_configured"))
                self.add(d, {})
                return ok(self.step("f0", "finish", result={"instance_id": d}))
            if d != "jellyfin":
                return self.reply(400, b"Unknown provider domain")
            MA["flows"]["f1"] = ("setup", d, None)
            return ok(self.step("f1", "form", entries=[{"key": k} for k in ("url", "username", "password", "verify_ssl")]))
        if c == "config/providers/reconfigure":
            MA["flows"]["f2"] = ("reconfigure", "jellyfin", a["instance_id"])
            return ok(self.step("f2", "form", entries=[{"key": k} for k in ("url", "username", "password", "verify_ssl")]))
        if c == "config/flows/submit":
            kind, d, iid = MA["flows"].pop(a["flow_id"])
            v = a["values"]
            if MA["fail_jellyfin"] or JF["users"].get(v.get("username"), {}).get("pw") != v.get("password"):
                MA["flows"][a["flow_id"]] = (kind, d, iid)
                return ok(self.step(a["flow_id"], "form", errors={"base": "auth_failed"}, entries=[{"key": k} for k in v]))
            if kind == "setup":
                iid = self.add(d, v)
            else:
                MA["providers"][iid].update(status="loaded", data=v)
            return ok(self.step(a["flow_id"], "finish", result={"instance_id": iid}))
        if c == "config/onboard_complete":
            if MA["onboard"]:
                return self.reply(400, b"Onboarding already completed")
            MA["onboard"] = True
            return ok(None)
        if c == "auth/tokens":
            return ok([{"token_id": v["id"], "name": v["name"]} for v in MA["tokens"].values() if v["user"] == tok["user"]])
        if c == "auth/token/revoke":
            for k, v in list(MA["tokens"].items()):
                if v["id"] == a["token_id"]:
                    del MA["tokens"][k]
            return ok(None)
        if c == "auth/token/create":
            return ok(new_token(tok["user"], a["name"], long_lived=True))
        self.reply(400, f"Invalid Command: {c}".encode())

    def add(self, domain, values):
        iid = f"{domain}--{len(MA['providers'])}"
        MA["providers"][iid] = {"instance_id": iid, "domain": domain, "status": "loaded", "data": values}
        MA["onboard"] = True  # MA marks onboarding complete with the first provider (config/providers.py)
        return iid


class FakeJellyfin(Base):
    def token(self):
        a = self.headers.get("Authorization") or ""
        if 'Client="' not in a:
            return None
        return next((u for u in JF["users"].values() if f'Token="tok-{u["Id"]}"' in a), None)

    def do_GET(self):
        if not JF["up"]:
            return self.reply(503)
        if self.path == "/System/Info/Public":
            return self.reply(200, {"StartupWizardCompleted": True})
        u = self.token()
        if not u or not u["Policy"].get("IsAdministrator"):
            return self.reply(401)
        if self.path == "/Users":
            return self.reply(200, [{k: v for k, v in x.items() if k != "pw"} for x in JF["users"].values()])
        if self.path == "/Library/VirtualFolders":
            return self.reply(200, JF["folders"])
        self.reply(404)

    def do_POST(self):
        b = self.body()
        if self.path == "/Users/AuthenticateByName":
            u = JF["users"].get(b["Username"])
            if not u or u["pw"] != b["Pw"]:
                return self.reply(401)
            if u["Name"] == "admin":
                JF["admin_logins"] += 1
            return self.reply(200, {"AccessToken": f"tok-{u['Id']}"})
        u = self.token()
        if not u or not u["Policy"].get("IsAdministrator"):
            return self.reply(401)
        if self.path == "/Users/New":
            uid = f"u-{b['Name']}"
            JF["users"][b["Name"]] = {"Id": uid, "Name": b["Name"], "pw": b["Password"], "Policy": {"IsAdministrator": False, "IsHidden": False}}
            return self.reply(200, {"Id": uid, "Name": b["Name"], "Policy": JF["users"][b["Name"]]["Policy"]})
        if self.path.startswith("/Users/Password?userId="):
            uid = urllib.parse.parse_qs(urllib.parse.urlsplit(self.path).query)["userId"][0]
            next(x for x in JF["users"].values() if x["Id"] == uid)["pw"] = b["NewPw"]
            return self.reply(204)
        if self.path.endswith("/Policy"):
            uid = self.path.split("/")[2]
            next(x for x in JF["users"].values() if x["Id"] == uid)["Policy"] = b
            return self.reply(204)
        self.reply(404)


def serve(handler):
    srv = ThreadingHTTPServer(("127.0.0.1", 0), handler)
    srv.daemon_threads = True
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv


class PostDeploy(unittest.TestCase):
    def setUp(self):
        self.ma, self.jf = serve(FakeMA), serve(FakeJellyfin)
        for srv in (self.ma, self.jf):
            self.addCleanup(srv.server_close)
            self.addCleanup(srv.shutdown)

    def run_hook(self, ha_token="", jf_admin_pw=JF_ADMIN_PW, ma_pw=MA_PW, jellyfin=None):
        env = {"PATH": os.environ["PATH"], "LAIB_DOMAIN": "box.test", "MA_WAIT_SECONDS": "0.05",
               "MA_URL": f"http://127.0.0.1:{self.ma.server_port}", "JELLYFIN_URL": jellyfin or f"http://127.0.0.1:{self.jf.server_port}",
               "MA_ADMIN_USER": "laib", "JELLYFIN_ADMIN_USER": "admin", "LAIB_SECRET_MA_ADMIN_PASSWORD": ma_pw,
               "LAIB_SECRET_JELLYFIN_ADMIN_PASSWORD": jf_admin_pw, "LAIB_SECRET_JELLYFIN_MA_PASSWORD": JF_MA_PW,
               "LAIB_SECRET_HA_MUSIC_ASSISTANT_TOKEN": ha_token}
        p = subprocess.run([str(HOOK)], env=env, capture_output=True, text=True, timeout=60)
        handed = dict(l[len("laib-secret: "):].split("=", 1) for l in p.stdout.splitlines() if l.startswith("laib-secret: "))
        rest = "\n".join(l for l in (p.stdout + "\n" + p.stderr).splitlines() if not l.startswith("laib-secret: "))
        for value in [MA_PW, JF_ADMIN_PW, JF_MA_PW, ha_token, *handed.values()]:
            if value:
                self.assertNotIn(value, rest, "a password or a token in the hook's log (#214)")
        return p, handed, rest

    def providers(self, domain):
        return [p for p in MA["providers"].values() if p["domain"] == domain]

    def test_a_fresh_music_assistant_is_usable_without_a_form(self):
        fresh()
        p, handed, log = self.run_hook()
        self.assertEqual(p.returncode, 0, log)
        self.assertEqual(MA["users"], {"laib": MA_PW}, "the only built-in account is Laib's administrator")
        self.assertTrue(MA["onboard"], "onboarding left open: MA stays on SETUP REQUIRED")
        self.assertEqual(MA["core"]["webserver"]["external_url"], "https://musik.box.test",
                         "the Home Assistant sign-in would return the browser to the LAN address")
        self.assertEqual(len(self.providers("radiobrowser")), 1)
        (jf,) = self.providers("jellyfin")
        self.assertEqual((jf["data"]["url"], jf["data"]["username"]), (f"http://127.0.0.1:{self.jf.server_port}", "musicassistant"))
        acct = JF["users"]["musicassistant"]
        self.assertEqual(acct["pw"], JF_MA_PW)
        self.assertEqual((acct["Policy"]["IsAdministrator"], acct["Policy"]["IsHidden"], acct["Policy"]["EnableAllFolders"],
                          acct["Policy"]["EnabledFolders"]), (False, True, False, ["f-music"]), "more than the music library, or an admin")
        self.assertEqual(set(handed), {"HA_MUSIC_ASSISTANT_TOKEN"})
        t = MA["tokens"][handed["HA_MUSIC_ASSISTANT_TOKEN"]]
        self.assertEqual((t["user"], t["name"], t["long"]), ("laib", "Home Assistant (laib)", True))
        self.assertEqual([v["name"] for v in MA["tokens"].values()], ["Home Assistant (laib)"], "the hook's sessions were left open")

    def test_a_second_run_changes_nothing(self):
        fresh()
        _, handed, _ = self.run_hook()
        before = json.dumps(MA["providers"], sort_keys=True)
        JF["admin_logins"] = 0
        p, again, log = self.run_hook(ha_token=handed["HA_MUSIC_ASSISTANT_TOKEN"])
        self.assertEqual(p.returncode, 0, log)
        self.assertEqual(again, {}, "a working token was replaced")
        self.assertEqual(json.dumps(MA["providers"], sort_keys=True), before, "a provider added twice or touched")
        self.assertEqual(MA["setups"], 1)
        self.assertIn(handed["HA_MUSIC_ASSISTANT_TOKEN"], MA["tokens"], "Home Assistant's token was revoked")

    def test_a_refused_token_is_replaced_and_the_old_one_revoked(self):
        fresh()
        _, handed, _ = self.run_hook()
        old = handed["HA_MUSIC_ASSISTANT_TOKEN"]
        MA["tokens"][old]["user"] = "laib"   # still listed under its name, but Laib holds another value
        p, again, log = self.run_hook(ha_token="junk")
        self.assertEqual(p.returncode, 0, log)
        self.assertIn("HA_MUSIC_ASSISTANT_TOKEN", again)
        self.assertNotIn(old, MA["tokens"], "the old token of that name is still valid")

    def test_a_jellyfin_source_whose_login_broke_is_set_up_again(self):
        fresh()
        self.run_hook()
        (jf,) = self.providers("jellyfin")
        jf["status"] = "auth_required"
        JF["users"]["musicassistant"]["pw"] = "changed in Jellyfin"
        p, _, log = self.run_hook()
        self.assertEqual(p.returncode, 0, log)
        self.assertEqual(JF["users"]["musicassistant"]["pw"], JF_MA_PW)
        self.assertEqual([p["status"] for p in self.providers("jellyfin")], ["loaded"])

    def test_without_jellyfin_radio_still_comes_and_the_apply_stays_green(self):
        fresh(jellyfin=False)
        p, handed, log = self.run_hook()
        self.assertEqual(p.returncode, 0, log)
        self.assertEqual(self.providers("jellyfin"), [])
        self.assertEqual(len(self.providers("radiobrowser")), 1)
        self.assertIn("laib add media", log)
        self.assertIn("HA_MUSIC_ASSISTANT_TOKEN", handed)

    def test_a_jellyfin_source_music_assistant_refuses_is_red(self):
        fresh()
        MA["fail_jellyfin"] = True
        p, _, log = self.run_hook()
        self.assertNotEqual(p.returncode, 0)
        self.assertIn("Jellyfin", log)
        self.assertEqual(len(self.providers("radiobrowser")), 1, "one failed source stopped the others")

    def test_a_music_assistant_onboarded_by_hand_is_red_and_says_what_to_set(self):
        fresh(onboarded=("owner", "theirs"))
        p, handed, log = self.run_hook()
        self.assertNotEqual(p.returncode, 0)
        self.assertEqual(handed, {})
        self.assertIn("laib secret set MA_ADMIN_PASSWORD", log)
        self.assertEqual(MA["users"], {"owner": "theirs"}, "a second administrator next to the owner's")


if __name__ == "__main__":
    unittest.main()
