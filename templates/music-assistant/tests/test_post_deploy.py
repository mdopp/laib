"""#526: music-assistant's post-deploy makes Music Assistant usable after
`laib add`, against a fake Music Assistant 2.10 (POST /setup, /auth/login,
the JSON-RPC /api and its provider setup flows) and a fake Jellyfin.

Before, the template only started the container: MA stood on SETUP REQUIRED,
the owner had to make an MA account, connect Jellyfin with a Jellyfin login
and add the integration in Home Assistant by hand. ServiceBay had no Music
Assistant template; its rule (LLDAP/Authelia logins, service accounts minted
by the box) is the one kept here."""
import json, os, subprocess, tempfile, threading, unittest, urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

HOOK = Path(__file__).resolve().parent.parent / "post-deploy"
MA_PW, JF_ADMIN_PW, JF_MA_PW = "-ma-admin-pw-4R", "-jf-admin-pw-2W", "-jf-ma-pw-6T"
MA, JF = {}, {}


def fresh(onboarded=None, jellyfin=True, music=True):
    MA.clear()
    MA.update(users={}, tokens={}, n=0, providers={}, flows={}, onboard=False, core={}, logins=0, setups=0, logouts=0, fail_jellyfin=False,
              radios=[], dead=set(), abs_keys={"abs-api-key-1", "abs-api-key-2"})
    if onboarded:
        MA["users"][onboarded[0]] = onboarded[1]
    JF.clear()
    JF.update(up=jellyfin, users={"admin": {"Id": "u-admin", "Name": "admin", "pw": JF_ADMIN_PW, "Policy": {"IsAdministrator": True}}},
              folders=[{"Name": "Musik", "ItemId": "f-music", "CollectionType": "music", "Locations": ["/media/music"]},
                       {"Name": "Podcasts", "ItemId": "f-pod", "CollectionType": "music", "Locations": ["/media/podcasts"]},
                       {"Name": "Radio", "ItemId": "f-radio", "CollectionType": "music", "Locations": ["/media/radio"]}] if music else [],
              admin_logins=0)


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
            if d not in ("jellyfin", "audiobookshelf"):
                return self.reply(400, b"Unknown provider domain")
            MA["flows"]["f1"] = ("setup", d, None)
            return ok(self.step("f1", "form", entries=[{"key": k} for k in self.keys(d)]))
        if c == "config/providers/reconfigure":
            d = MA["providers"][a["instance_id"]]["domain"]
            MA["flows"]["f2"] = ("reconfigure", d, a["instance_id"])
            return ok(self.step("f2", "form", entries=[{"key": k} for k in self.keys(d)]))
        if c == "config/flows/submit":
            kind, d, iid = MA["flows"].pop(a["flow_id"])
            v = a["values"]
            refused = (v.get("api_token") not in MA["abs_keys"] or v.get("url") != "http://127.0.0.1:13378") if d == "audiobookshelf" else \
                (MA["fail_jellyfin"] or JF["users"].get(v.get("username"), {}).get("pw") != v.get("password"))
            if refused:
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
        if c == "builtin/add_radio":           # providers/builtin: the URL is the item id, MA probes the stream
            if a["url"] in MA["dead"]:
                return self.reply(500, b"Unable to retrieve info for stream")
            MA["radios"] = [r for r in MA["radios"] if r["url"] != a["url"]]
            MA["radios"].append({"url": a["url"], "name": a["name"], "image": a.get("image_url"), "favorite": False, "id": str(len(MA["radios"]) + 1)})
            return ok({"item_id": a["url"], "provider": "builtin", "name": a["name"], "media_type": "radio", "uri": "builtin://radio/" + a["url"]})
        if c == "music/radios/library_items":
            return ok([{"item_id": r["id"], "provider": "library", "name": r["name"], "favorite": r["favorite"], "uri": "library://radio/" + r["id"],
                        "provider_mappings": [{"item_id": r["url"], "provider_domain": "builtin", "provider_instance": "builtin"}]} for r in MA["radios"]])
        if c == "music/favorites/add_item":
            u = a["item"]
            r = next(r for r in MA["radios"] if u in ("builtin://radio/" + r["url"], "library://radio/" + r["id"]))
            r["favorite"] = True
            return ok(None)
        self.reply(400, f"Invalid Command: {c}".encode())

    def keys(self, d):
        return ("url", "username", "password", "api_token", "verify_ssl") if d == "audiobookshelf" else ("url", "username", "password", "verify_ssl")

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


M3U = """#EXTM3U
#EXTINF:-1 tvg-logo="https://img.example.org/welle-eins.png" group-title="Erfunden, öffentlich", Welle Eins
https://stream.example.org/welle-eins/mp3/128/stream.mp3

#EXTINF:-1 group-title="Erfunden", Radio Möwe, Nord
https://radio-moewe.example.net/live

#EXTINF:-1 tvg-logo="https://img.example.org/still.png",Stille Station
https://still.example.com/dead.mp3
"""


class PostDeploy(unittest.TestCase):
    def setUp(self):
        self.ma, self.jf = serve(FakeMA), serve(FakeJellyfin)
        for srv in (self.ma, self.jf):
            self.addCleanup(srv.server_close)
            self.addCleanup(srv.shutdown)
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.radio = Path(self.tmp.name, "file-share/data/radio")
        self.radio.mkdir(parents=True)
        Path(self.tmp.name, "music-assistant/data").mkdir(parents=True)

    def run_hook(self, ha_token="", jf_admin_pw=JF_ADMIN_PW, ma_pw=MA_PW, jellyfin=None, abs_key=""):
        env = {"PATH": os.environ["PATH"], "LAIB_DOMAIN": "box.test", "MA_WAIT_SECONDS": "0.05", "LAIB_DATA_DIR": self.tmp.name,
               "LAIB_SECRET_ABS_MA_TOKEN": abs_key, "ABS_URL": "http://127.0.0.1:13378",
               "MA_URL": f"http://127.0.0.1:{self.ma.server_port}", "JELLYFIN_URL": jellyfin or f"http://127.0.0.1:{self.jf.server_port}",
               "MA_ADMIN_USER": "laib", "JELLYFIN_ADMIN_USER": "admin", "LAIB_SECRET_MA_ADMIN_PASSWORD": ma_pw,
               "LAIB_SECRET_JELLYFIN_ADMIN_PASSWORD": jf_admin_pw, "LAIB_SECRET_JELLYFIN_MA_PASSWORD": JF_MA_PW,
               "LAIB_SECRET_HA_MUSIC_ASSISTANT_TOKEN": ha_token}
        p = subprocess.run([str(HOOK)], env=env, capture_output=True, text=True, timeout=60)
        handed = dict(l[len("laib-secret: "):].split("=", 1) for l in p.stdout.splitlines() if l.startswith("laib-secret: "))
        rest = "\n".join(l for l in (p.stdout + "\n" + p.stderr).splitlines() if not l.startswith("laib-secret: "))
        for value in [MA_PW, JF_ADMIN_PW, JF_MA_PW, ha_token, abs_key, *handed.values()]:
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
                          acct["Policy"]["EnabledFolders"]), (False, True, False, ["f-music"]),
                         "more than the music library (podcasts and radio are no music, #646), or an admin")
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


    # #646: the house's stations, as the share holds them (radio/*.m3u, *.strm), become Music Assistant's
    # radio favourites once; the Jellyfin library "Radio" can then go
    def test_the_shares_stations_become_radio_favourites_once(self):
        fresh()
        (self.radio / "sender.m3u").write_text(M3U)
        (self.radio / "Welle Eins.strm").write_text("https://stream.example.org/welle-eins/mp3/128/stream.mp3\n")   # the same as in the m3u
        (self.radio / "Kleiner Sender.strm").write_text("https://klein.example.org/stream\n")
        (self.radio / "kaputt.strm").write_text("keine adresse\n")
        MA["dead"].add("https://still.example.com/dead.mp3")
        p, _, log = self.run_hook()
        self.assertEqual(p.returncode, 0, log)
        got = {r["name"]: r for r in MA["radios"]}
        self.assertEqual(set(got), {"Welle Eins", "Radio Möwe, Nord", "Kleiner Sender"})
        self.assertEqual(got["Welle Eins"]["image"], "https://img.example.org/welle-eins.png")
        self.assertTrue(all(r["favorite"] for r in MA["radios"]), "in the library, but no favourite")
        self.assertIn("Stille Station", log, "a station Music Assistant refused is not named")
        MA["radios"] = [r for r in MA["radios"] if r["name"] != "Kleiner Sender"]   # the family removed one in MA
        p, _, log = self.run_hook()
        self.assertEqual(p.returncode, 0, log)
        self.assertEqual({r["name"] for r in MA["radios"]}, {"Welle Eins", "Radio Möwe, Nord"}, "a removed station came back")

    def test_audiobookshelf_becomes_a_source_with_the_key_it_handed_over(self):
        fresh()
        p, _, log = self.run_hook()
        self.assertEqual((p.returncode, self.providers("audiobookshelf")), (0, []), log)
        self.assertIn("laib add audiobookshelf", log)
        p, _, log = self.run_hook(abs_key="abs-api-key-1")
        self.assertEqual(p.returncode, 0, log)
        (a,) = self.providers("audiobookshelf")
        self.assertEqual((a["data"]["url"], a["data"]["api_token"]), ("http://127.0.0.1:13378", "abs-api-key-1"))
        p, _, log = self.run_hook(abs_key="abs-api-key-1")
        self.assertEqual((p.returncode, len(self.providers("audiobookshelf"))), (0, 1), log)
        # ABS minted a new key (the old one refused): MA takes it, still one source
        p, _, log = self.run_hook(abs_key="abs-api-key-2")
        self.assertEqual(p.returncode, 0, log)
        (a,) = self.providers("audiobookshelf")
        self.assertEqual(a["data"]["api_token"], "abs-api-key-2")


if __name__ == "__main__":
    unittest.main()
