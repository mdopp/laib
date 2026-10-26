"""#256: Jellyfin logs the family in with their LLDAP accounts, as on
ServiceBay (templates/media/post-deploy.py:679-800 there): the
LDAP-Authentication plugin, installed and its configuration rewritten on
every apply, users created on their first login, the admins group mapped to
Jellyfin administrators, the local admin kept as break-glass, Quick Connect
on. The plugin binds with a read-only LLDAP account (as radicale does since
#55), never the LLDAP admin as ServiceBay did. A fake Jellyfin answers like
Jellyfin 12, which ignores X-Emby-Authorization (ServiceBay #3048)."""
import socket
import json, os, re, subprocess, sys, tempfile, threading, unittest, urllib.parse
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

HOOK = Path(__file__).resolve().parent.parent / "post-deploy"
LDAP_PW, ADMIN_PW, HA_PW = "-ldap-ro-pw-8K", "admin-pw-3J", "ha-svc-pw-7Q"
PLUGIN = "958aad6637844d2ab89aa7b6fab6e25c"
JF = {}
SIDE = {}   # what Audiobookshelf and Music Assistant answer media's ordering guard (#646)


def fresh():
    JF.clear()
    JF.update(wizard=False, users={}, folders=[], plugins=[], pending=False, restarts=0, installs=0, starting=0, wizard503=0, created=0,
              config={"QuickConnectAvailable": False, "ServerName": "x"}, locations={}, options={}, option_posts=0, types={}, deleted=[],
              media={"music", "movies"}, tokens={}, minted=0)   # the folders Jellyfin sees under its own /media mount (#651)
    SIDE.clear()
    SIDE.update(abs_token="abs-key-1", ma_token="ma-tok-1", abs_libs=[], abs_totals={}, radios=0)


class FakeJellyfin(BaseHTTPRequestHandler):
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
        return json.loads(self.rfile.read(int(self.headers.get("Content-Length") or 0)) or b"{}")

    def client(self):
        """Jellyfin 12 reads the client id from Authorization only (#3048 there)"""
        return 'Client="' in (self.headers.get("Authorization") or "")

    def token(self):
        return 'Token="tok"' in (self.headers.get("Authorization") or "")

    def bearer(self):
        m = re.search(r'Token="([^"]*)"', self.headers.get("Authorization") or "")
        return m and m.group(1)

    def do_GET(self):
        if self.path == "/Users/Me" and JF.get("drop_me", 0) > 0:   # Jellyfin restarting under the apply (#756)
            JF["drop_me"] -= 1
            self.close_connection = True
            self.connection.shutdown(socket.SHUT_RDWR)
            return
        if self.path == "/Users/Me":                 # whose token this is; a non-admin user's own token answers too
            t = self.bearer()
            if t == "tok":
                return self.reply(200, JF["users"]["admin"])
            if t not in JF["tokens"]:
                return self.reply(401)
            return self.reply(200, next(u for u in JF["users"].values() if u["Name"] == JF["tokens"][t][0]))
        if self.path == "/health":
            return self.reply(200)
        if self.path == "/System/Info/Public":
            if JF["starting"]:                       # /health is up, the server is not (vm-install run 39030739987)
                JF["starting"] -= 1
                return self.reply(503)
            return self.reply(200, {"StartupWizardCompleted": JF["wizard"]})
        if self.path == "/Startup/User":
            return self.reply(200, {"Name": "root"})
        if not self.token():
            return self.reply(401)
        if self.path.startswith("/Environment/DirectoryContents?"):   # Jellyfin's file browser, admin only
            q = urllib.parse.parse_qs(urllib.parse.urlparse(self.path).query)
            if q.get("path") != ["/media"] or q.get("includeDirectories") != ["true"]:
                return self.reply(400)
            return self.reply(200, [{"Name": d, "Path": "/media/" + d, "Type": "Directory"} for d in sorted(JF["media"])])
        if self.path == "/Library/VirtualFolders":
            return self.reply(200, [{"Name": n, "ItemId": i, "Locations": JF["locations"].get(i, []), "CollectionType": JF["types"].get(i),
                                     "LibraryOptions": JF["options"].get(i, {})} for n, i in JF["folders"]])
        if self.path == "/Users":
            return self.reply(200, list(JF["users"].values()))
        if self.path == "/Plugins":
            return self.reply(200, JF["plugins"])
        if self.path == "/System/Configuration":
            return self.reply(200, JF["config"])
        self.reply(404)

    def do_POST(self):
        b = self.body()
        if not self.client():
            return self.reply(400, {"error": "no client identity"})
        p = self.path
        if p.startswith("/Startup/") and JF["wizard503"]:  # vm-install run 39031896537: Info/Public 200, the wizard 503
            JF["wizard503"] -= 1
            return self.reply(503)
        if p.startswith("/Startup/"):
            if p == "/Startup/User":
                JF["users"][b["Name"]] = {"Name": b["Name"], "Id": "u-admin", "pw": b["Password"]}
            if p == "/Startup/Complete":
                JF["wizard"] = True
            return self.reply(204)
        if p == "/Users/AuthenticateByName":
            u = JF["users"].get(b["Username"])
            if not u or u["pw"] != b["Pw"] or (u.get("Policy") or {}).get("IsDisabled"):
                return self.reply(401)
            if u["Id"] == "u-admin":
                return self.reply(200, {"AccessToken": "tok"})
            # any other user gets a token of its own; Jellyfin logs out the one this user's device held before
            dev = re.search(r'DeviceId="([^"]*)"', self.headers["Authorization"]).group(1)
            JF["tokens"] = {t: v for t, v in JF["tokens"].items() if v != (u["Name"], dev)}
            JF["minted"] += 1
            t = f"jf-{u['Name']}-{JF['minted']}"
            JF["tokens"][t] = (u["Name"], dev)
            return self.reply(200, {"AccessToken": t})
        if not self.token():
            return self.reply(401)
        if p.startswith("/Library/VirtualFolders?"):
            name = re.search(r"name=([^&]+)", p).group(1)
            i = f"lib-{len(JF['folders']) + 1}"
            JF["folders"].append((name, i))
            JF["locations"][i] = [x["Path"] for x in b["LibraryOptions"]["PathInfos"]]
            JF["options"][i] = b["LibraryOptions"]
            JF["types"][i] = re.search(r"collectionType=([^&]+)", p).group(1)
            return self.reply(204)
        if p == "/Library/VirtualFolders/LibraryOptions":
            JF["option_posts"] += 1
            JF["options"][b["Id"]] = b["LibraryOptions"]
            return self.reply(204)
        if p.startswith("/Packages/Installed/LDAP%20Authentication"):
            JF["installs"] += 1
            JF["pending"] = True
            return self.reply(204)
        if p == "/System/Restart":
            JF["restarts"] += 1
            if JF["pending"]:
                JF["plugins"], JF["pending"] = [{"Name": "LDAP-Auth", "Id": PLUGIN, "Status": "Active"}], False
            return self.reply(204)
        if p == "/System/Configuration":
            JF["config"] = b
            return self.reply(204)
        if p == "/Users/New":
            JF["created"] += 1
            JF["users"][b["Name"]] = {"Name": b["Name"], "Id": "u-" + b["Name"], "pw": b["Password"],
                                      "Policy": {"IsAdministrator": False, "EnableAllFolders": True, "EnabledFolders": [],
                                                 "AuthenticationProviderId": "Default", "PasswordResetProviderId": "Default"}}
            return self.reply(200, JF["users"][b["Name"]])
        if p.startswith("/Users/Password?userId="):   # Jellyfin 10.9+: an admin sets another user's password without the old one
            u = next(u for u in JF["users"].values() if u["Id"] == p.split("=", 1)[1])
            if u["Id"] == "u-admin" and b.get("CurrentPw") != u["pw"]:   # its own only with the current one
                return self.reply(403)
            u["pw"] = b["NewPw"]
            return self.reply(204)
        if m := re.fullmatch(r"/Users/([^/]+)/Policy", p):
            if not b.get("AuthenticationProviderId") or not b.get("PasswordResetProviderId"):
                return self.reply(400, {"error": "a policy without its providers"})
            next(u for u in JF["users"].values() if u["Id"] == m.group(1))["Policy"] = b
            return self.reply(204)
        self.reply(404)


    def do_DELETE(self):
        if not self.client():
            return self.reply(400, {"error": "no client identity"})
        if not self.token():
            return self.reply(401)
        if m := re.fullmatch(r"/Library/VirtualFolders\?name=([^&]+)&refreshLibrary=false", self.path):
            name = urllib.parse.unquote(m.group(1))
            if not any(n == name for n, _ in JF["folders"]):
                return self.reply(404)
            JF["folders"] = [(n, i) for n, i in JF["folders"] if n != name]
            JF["deleted"].append(name)
            return self.reply(204)
        self.reply(404)


class FakeSide(BaseHTTPRequestHandler):
    """Audiobookshelf (/api/libraries with MA's API key) and Music Assistant (/api with HA's token), as far as
    media's guard asks them: are the books, the podcasts and the radio there before Jellyfin lets them go"""

    def log_message(self, *a):
        pass

    def reply(self, code, body=None):
        data = json.dumps(body).encode() if body is not None else b""
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if self.headers.get("Authorization") != "Bearer " + SIDE["abs_token"]:
            return self.reply(401)
        if self.path == "/api/libraries":
            return self.reply(200, {"libraries": SIDE["abs_libs"]})
        if m := re.fullmatch(r"/api/libraries/([^/?]+)/items\?limit=1", self.path):
            return self.reply(200, {"results": [], "total": SIDE["abs_totals"].get(m.group(1), 0)})
        self.reply(404)

    def do_POST(self):
        b = json.loads(self.rfile.read(int(self.headers.get("Content-Length") or 0)) or b"{}")
        if self.path != "/api" or self.headers.get("Authorization") != "Bearer " + SIDE["ma_token"]:
            return self.reply(401)
        if b.get("command") == "music/radios/count":
            return self.reply(200, SIDE["radios"])
        self.reply(400)


class PostDeploy(unittest.TestCase):
    def setUp(self):
        fresh()
        self.srv = HTTPServer(("127.0.0.1", 0), FakeJellyfin)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.addCleanup(self.srv.shutdown)
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        # the hook container does not mount the share (#651): what exists is what Jellyfin sees, JF["media"]
        self.media = Path(self.tmp.name, "share-not-mounted-here")
        self.xml = Path(self.tmp.name, "media/jellyfin-config/plugins/configurations/LDAP-Auth.xml")
        self.side = HTTPServer(("127.0.0.1", 0), FakeSide)
        threading.Thread(target=self.side.serve_forever, daemon=True).start()
        self.addCleanup(self.side.shutdown)

    def run_hook(self, **extra):
        side = f"http://127.0.0.1:{self.side.server_port}"
        env = dict(os.environ, ABS_URL=side, MA_URL=side, **extra, JELLYFIN_URL=f"http://127.0.0.1:{self.srv.server_port}", LAIB_DATA_DIR=self.tmp.name,
                   MEDIA_ROOT=str(self.media), BASE_DN="dc=vm,dc=test", ADMIN_USER="admin",
                   LAIB_SECRET_JELLYFIN_ADMIN_PASSWORD=ADMIN_PW, LAIB_SECRET_JELLYFIN_LDAP_PASSWORD=LDAP_PW)
        return subprocess.run([sys.executable, str(HOOK)], env=env, capture_output=True, text=True, timeout=60)

    def test_a_fresh_jellyfin_logs_the_family_in_through_lldap(self):
        p = self.run_hook()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        x = self.xml.read_text()
        for want in ("<LdapServer>host.containers.internal</LdapServer>", "<LdapPort>3890</LdapPort>",
                     "<LdapBindUser>uid=jellyfin,ou=people,dc=vm,dc=test</LdapBindUser>",
                     f"<LdapBindPassword>{LDAP_PW}</LdapBindPassword>", "<LdapBaseDn>ou=people,dc=vm,dc=test</LdapBaseDn>",
                     "memberOf=cn=family,ou=groups,dc=vm,dc=test", "<LdapAdminFilter>(memberOf=cn=admins,ou=groups,dc=vm,dc=test)</LdapAdminFilter>",
                     "<CreateUsersFromLdap>true</CreateUsersFromLdap>", "<EnableAllFolders>false</EnableAllFolders>",
                     "<string>lib-1</string>", "<string>lib-2</string>"):
            self.assertIn(want, x)
        self.assertNotIn("uid=admin", x, "the plugin binds as the LLDAP admin")
        self.assertEqual((JF["installs"], JF["restarts"]), (1, 1), "the plugin was not installed and loaded")
        self.assertTrue(JF["config"]["QuickConnectAvailable"])
        self.assertEqual(set(JF["users"]), {"admin", "haus"}, "a shared login was created next to the family's own")   # haus: Haus' own, hidden (#695)
        self.assertNotIn(LDAP_PW, p.stdout + p.stderr)
        p = self.run_hook()
        self.assertEqual((p.returncode, JF["installs"], JF["restarts"]), (0, 1, 1), p.stdout + p.stderr)

    def test_a_rotation_changes_the_admin_password_in_jellyfin_and_hands_it_back(self):
        # #665: the admin password carried from ServiceBay, rotated: Jellyfin takes it first, then Laib
        self.run_hook()
        new = "-jf-admin-rotated-665"
        p = self.run_hook(LAIB_ROTATE_JELLYFIN_ADMIN_PASSWORD=new)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertIn("laib-secret: JELLYFIN_ADMIN_PASSWORD=" + new, p.stdout.splitlines())
        self.assertEqual(JF["users"]["admin"]["pw"], new, "Jellyfin still has the old one")
        self.assertNotIn(new, "\n".join(l for l in (p.stdout + p.stderr).splitlines() if not l.startswith("laib-secret: ")))
        p = self.run_hook(LAIB_ROTATE_JELLYFIN_ADMIN_PASSWORD=new)   # again, before laib stored it: only handed back
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertIn("laib-secret: JELLYFIN_ADMIN_PASSWORD=" + new, p.stdout.splitlines())

    def test_a_rotation_jellyfin_refuses_keeps_the_old_one(self):
        self.run_hook()
        JF["users"]["admin"]["pw"] = "changed in the app"
        p = self.run_hook(LAIB_ROTATE_JELLYFIN_ADMIN_PASSWORD="-jf-admin-rotated-665")
        self.assertNotEqual(p.returncode, 0)
        self.assertNotIn("laib-secret: JELLYFIN_ADMIN_PASSWORD", p.stdout)
        self.assertIn("NOT rotated", p.stderr)

    def test_a_dropped_connection_while_jellyfin_restarts_is_tried_again(self):
        """vm-install, train/2026-10-26j: the second apply's Haus token check met a connection reset (#756)"""
        p = self.run_hook()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        tok = self.handed(p).get("HAUS_JELLYFIN_TOKEN")
        JF["drop_me"] = 2
        p = self.run_hook(LAIB_SECRET_HAUS_JELLYFIN_TOKEN=tok)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertEqual(JF["drop_me"], 0, "the drop did not happen")
        self.assertNotIn("HAUS_JELLYFIN_TOKEN", self.handed(p), "a working token was minted again after a dropped connection")

    def test_an_empty_jellyfin_gets_musik_filme_serien_from_what_it_sees(self):
        # #651: the hook asked os.path.isdir in its own container, where the share is not mounted, so a new
        # box never got a library. Jellyfin's own /media decides; a folder it does not see gets none.
        JF["media"] = {"music", "movies", "tv", "audiobooks"}
        p = self.run_hook()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertEqual(self.names(), {"Musik", "Filme", "Serien"})
        self.assertEqual(sorted(l for ls in JF["locations"].values() for l in ls), ["/media/movies", "/media/music", "/media/tv"])
        self.assertTrue(JF["options"]["lib-1"]["EnableRealtimeMonitor"])
        p = self.run_hook()
        self.assertEqual((p.returncode, len(JF["folders"])), (0, 3), p.stdout + p.stderr)
        JF.update(folders=[], locations={}, options={}, types={}, media={"music"})
        p = self.run_hook()
        self.assertEqual((p.returncode, self.names()), (0, {"Musik"}), p.stdout + p.stderr)

    def test_the_owners_libraries_stay_as_they_are(self):
        # #651: a box with ServiceBay's libraries gets no second one on the same folder
        JF.update(wizard=True, users={"admin": {"Name": "admin", "Id": "u-admin", "pw": ADMIN_PW}},
                  plugins=[{"Name": "LDAP-Auth", "Id": PLUGIN, "Status": "Active"}], media={"music", "movies", "tv"},
                  folders=[("Music", "sb-1"), ("Movies", "sb-2"), ("Shows", "sb-3")], types={"sb-1": "music", "sb-2": "movies", "sb-3": "tvshows"},
                  locations={"sb-1": ["/media/music"], "sb-2": ["/media/movies/"], "sb-3": ["/media/tv"]},
                  options={k: {"EnableRealtimeMonitor": True} for k in ("sb-1", "sb-2", "sb-3")})
        p = self.run_hook()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertEqual(self.names(), {"Music", "Movies", "Shows"}, "a second library on the owner's folder")

    def test_servicebays_plugin_config_is_rebound_and_keeps_its_libraries(self):
        JF.update(wizard=True, users={"admin": {"Name": "admin", "Id": "u-admin", "pw": ADMIN_PW}},
                  folders=[("Music", "sb-public-1"), ("Music (anna)", "sb-anna-1")],
                  plugins=[{"Name": "LDAP-Auth", "Id": PLUGIN, "Status": "Active"}])
        self.xml.parent.mkdir(parents=True)
        self.xml.write_text("<PluginConfiguration><LdapBindUser>uid=admin,ou=people,dc=vm,dc=test</LdapBindUser>"
                            "<EnabledFolders><string>sb-public-1</string></EnabledFolders></PluginConfiguration>")
        p = self.run_hook()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        x = self.xml.read_text()
        self.assertIn("uid=jellyfin,ou=people", x)
        self.assertIn("<string>sb-public-1</string>", x, "the libraries ServiceBay gave new users are gone")
        self.assertNotIn("sb-anna-1", x, "anna's private library is open to every new user")
        self.assertEqual((JF["installs"], JF["restarts"]), (0, 1), "a changed config must be loaded, an installed plugin not installed again")

    def test_home_assistant_gets_its_own_account_not_a_family_login(self):
        # #375: HA's Jellyfin entry logged in as the LLDAP user solaris, which the family/admins filter refuses.
        # Its own account is Jellyfin's, hidden, no administrator, the family's libraries; not in LLDAP.
        p = self.run_hook(LAIB_SECRET_JELLYFIN_HA_PASSWORD=HA_PW)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        u = JF["users"].get("homeassistant")
        self.assertIsNotNone(u, "no account for Home Assistant")
        self.assertEqual(u["pw"], HA_PW)
        self.assertFalse(u["Policy"]["IsAdministrator"])
        self.assertTrue(u["Policy"].get("IsHidden"), "the service account shows on the TV's login screen")
        self.assertEqual((u["Policy"]["EnableAllFolders"], u["Policy"]["EnabledFolders"]), (False, ["lib-1", "lib-2"]))
        self.assertIn("homeassistant", p.stdout)
        self.assertNotIn(HA_PW, p.stdout + p.stderr)
        # the next apply changes nothing; a new secret is the account's new password
        p = self.run_hook(LAIB_SECRET_JELLYFIN_HA_PASSWORD=HA_PW)
        self.assertEqual((p.returncode, JF["created"]), (0, 2), p.stdout + p.stderr)   # homeassistant and haus (#695), once each
        p = self.run_hook(LAIB_SECRET_JELLYFIN_HA_PASSWORD="ha-svc-pw-new")
        self.assertEqual((p.returncode, JF["users"]["homeassistant"]["pw"]), (0, "ha-svc-pw-new"), p.stdout + p.stderr)

    def test_the_music_library_watches_its_folder(self):
        # #639: what the Musik-Eingang imports shows without a scan. A library from ServiceBay ("Music", by its
        # folder) without the realtime monitor gets it, its other options kept; the next apply changes nothing
        JF.update(wizard=True, users={"admin": {"Name": "admin", "Id": "u-admin", "pw": ADMIN_PW}},
                  folders=[("Music", "sb-1"), ("Filme", "sb-2")], plugins=[{"Name": "LDAP-Auth", "Id": PLUGIN, "Status": "Active"}],
                  locations={"sb-1": ["/media/music"], "sb-2": ["/media/movies"]},
                  options={"sb-1": {"EnableRealtimeMonitor": False, "PreferredMetadataLanguage": "de"}, "sb-2": {}})
        p = self.run_hook()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertEqual(JF["options"]["sb-1"], {"EnableRealtimeMonitor": True, "PreferredMetadataLanguage": "de"})
        self.assertEqual(JF["options"]["sb-2"], {}, "only the music library is touched")
        self.assertEqual(JF["option_posts"], 1)
        p = self.run_hook()
        self.assertEqual((p.returncode, JF["option_posts"]), (0, 1), p.stdout + p.stderr)

    # #695: Haus checks a wished song against the household's music, read only. Jellyfin's API keys have no
    # levels (each acts as an administrator), so Haus gets the token of a Jellyfin user of its own, `haus`:
    # hidden, no administrator, the music libraries only, no playback, download or deletion.
    def handed(self, p):
        return dict(re.findall(r"^laib-secret: ([A-Z_]+)=(\S+)$", p.stdout, re.M))

    def test_haus_gets_a_music_only_account_and_its_token(self):
        JF["media"] = {"music", "movies", "tv"}
        p = self.run_hook()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        u = JF["users"].get("haus")
        self.assertIsNotNone(u, "no Jellyfin account for Haus")
        pol = u["Policy"]
        self.assertFalse(pol["IsAdministrator"], "Haus reads as a Jellyfin administrator")
        self.assertTrue(pol["IsHidden"], "Haus' account shows on the TV's login screen")
        musik = next(i for n, i in JF["folders"] if n == "Musik")
        self.assertEqual((pol["EnableAllFolders"], pol["EnabledFolders"]), (False, [musik]), "Haus sees more than the music")
        for no in ("EnableMediaPlayback", "EnableContentDownloading", "EnableContentDeletion", "EnableRemoteControlOfOtherUsers"):
            self.assertIs(pol.get(no), False, f"Haus' account keeps {no}")
        tok = self.handed(p).get("HAUS_JELLYFIN_TOKEN")
        self.assertIn(tok, JF["tokens"], "the handed-back token is none Jellyfin knows")
        self.assertEqual(JF["tokens"][tok][0], "haus", "the token is not Haus' account's")
        self.assertNotEqual(JF["tokens"][tok][1], "laib-post-deploy", "Haus' token shares the hook's device, the hook's next login logs it out")
        self.assertIn("HAUS_JELLYFIN_TOKEN minted (new)", p.stdout)
        # a token that still works is kept: no new one, none handed back, the old one valid
        p = self.run_hook(LAIB_SECRET_HAUS_JELLYFIN_TOKEN=tok)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertNotIn("HAUS_JELLYFIN_TOKEN", self.handed(p))
        self.assertNotIn(" minted", p.stdout)
        self.assertIn(tok, JF["tokens"])
        self.assertEqual(JF["created"], 1, "a second account for Haus")
        # one Jellyfin refuses (its device removed in the dashboard) is replaced
        JF["tokens"].pop(tok)
        p = self.run_hook(LAIB_SECRET_HAUS_JELLYFIN_TOKEN=tok)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        new = self.handed(p).get("HAUS_JELLYFIN_TOKEN")
        self.assertTrue(new and new != tok and JF["tokens"].get(new, ("",))[0] == "haus", p.stdout)
        self.assertIn("HAUS_JELLYFIN_TOKEN minted (replaces one Jellyfin refuses)", p.stdout)
        self.assertEqual([t for t, v in JF["tokens"].items() if v[0] == "haus"], [new], "an old token of Haus still works")

    def test_an_existing_haus_account_is_cut_back_to_the_music(self):
        # one made by hand, an administrator who sees everything, gets the music only; spoken word is no music (#646)
        ids = self.box()
        JF["users"]["haus"] = {"Name": "haus", "Id": "u-haus", "pw": "by-hand", "Policy": {
            "IsAdministrator": True, "EnableAllFolders": True, "EnabledFolders": [], "EnableMediaPlayback": True,
            "AuthenticationProviderId": "Default", "PasswordResetProviderId": "Default"}}
        p = self.run_hook()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        pol = JF["users"]["haus"]["Policy"]
        self.assertFalse(pol["IsAdministrator"] or pol["EnableAllFolders"] or pol["EnableMediaPlayback"])
        self.assertEqual(sorted(pol["EnabledFolders"]), sorted([ids["Music"], ids["Music (cdopp)"]]),
                         "Haus sees other libraries than the music")
        self.assertEqual(JF["created"], 0)
        self.assertIn("HAUS_JELLYFIN_TOKEN", self.handed(p))
        self.assertNotIn("by-hand", p.stdout + p.stderr)

    def test_a_server_still_starting_behind_a_green_health_is_waited_for(self):
        JF["starting"], JF["wizard503"] = 3, 2
        p = self.run_hook()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertTrue(JF["wizard"])

    def test_a_plugin_that_does_not_come_up_is_red(self):
        JF.update(wizard=True, users={"admin": {"Name": "admin", "Id": "u-admin", "pw": ADMIN_PW}},
                  plugins=[{"Name": "LDAP-Auth", "Id": PLUGIN, "Status": "Malfunctioned"}])
        p = self.run_hook()
        self.assertNotEqual(p.returncode, 0, "the family cannot log in, yet the hook is green")
        self.assertIn("LDAP", p.stderr)


    # #646: ServiceBay registered most libraries twice ("Music" and "Music2" on one folder); Symfonium's shuffle
    # sees every one. The house as it is on the box, with made-up ids, and the twins that only look alike.
    def box(self, *more):
        libs = [("Music", "music", ["/media/music"]), ("Music2", "music", ["/media/music"]),
                ("Music (cdopp)", "music", ["/media/cdopp/music"]), ("Music (cdopp)2", "music", ["/media/cdopp/music"]),
                ("Audiobooks", "books", ["/media/audiobooks"]), ("Audiobooks2", "books", ["/media/audiobooks"]),
                ("Podcasts", "music", ["/media/podcasts"]), ("Radio", "music", ["/media/radio"]),
                ("Movies", "movies", ["/media/movies"]), ("Movies2", "movies", ["/media/movies"]), ("Movies (mdopp)", "movies", ["/media/mdopp/movies"]),
                ("Shows", "tvshows", ["/media/tv"]), ("Shows2", "tvshows", ["/media/tv"]), *more]
        JF.update(wizard=True, plugins=[{"Name": "LDAP-Auth", "Id": PLUGIN, "Status": "Active"}],
                  users={"admin": {"Name": "admin", "Id": "u-admin", "pw": ADMIN_PW}},
                  folders=[(n, f"id-{k}") for k, (n, _, _) in enumerate(libs)],
                  types={f"id-{k}": t for k, (_, t, _) in enumerate(libs)},
                  locations={f"id-{k}": locs for k, (_, _, locs) in enumerate(libs)},
                  options={f"id-{k}": {"EnableRealtimeMonitor": True} for k in range(len(libs))})
        return {n: f"id-{k}" for k, (n, _, _) in enumerate(libs)}

    def names(self):
        return {n for n, _ in JF["folders"]}

    def test_servicebays_twins_go_and_who_saw_one_sees_the_other(self):
        ids = self.box(("Hörspiele2", "books", ["/media/hoerspiele"]),            # a "2" without a twin
                       ("Filme", "movies", ["/media/filme"]), ("Filme2", "movies", ["/media/filme-alt"]),   # its twin's name, other folders
                       ("Musik", "music", ["/media/music"]),                       # the same folder, but not the name plus "2"
                       ("Krimis", "books", ["/media/krimis"]), ("Krimis2", "music", ["/media/krimis"]),     # same folder, another kind
                       ("Leer", "music", []), ("Leer2", "music", []))              # no folders: nothing says they are the same
        JF["users"]["anna"] = {"Name": "anna", "Id": "u-anna", "pw": "x", "Policy": {
            "EnableAllFolders": False, "EnabledFolders": [ids["Music2"], ids["Shows"], ids["Shows2"]],
            "AuthenticationProviderId": "LDAP", "PasswordResetProviderId": "Default"}}
        self.xml.parent.mkdir(parents=True)
        self.xml.write_text(f"<PluginConfiguration><EnabledFolders><string>{ids['Music2']}</string></EnabledFolders></PluginConfiguration>")
        p = self.run_hook()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertEqual(sorted(JF["deleted"]), ["Audiobooks2", "Movies2", "Music (cdopp)2", "Music2", "Shows2"])
        for kept in ("Hörspiele2", "Filme2", "Musik", "Krimis2", "Leer2", "Music", "Music (cdopp)", "Audiobooks", "Podcasts", "Radio"):
            self.assertIn(kept, self.names())
        self.assertIn("library removed: Music2", p.stdout)
        self.assertIn("/media/music", p.stdout, "the log does not say why it was a twin")
        self.assertEqual(JF["users"]["anna"]["Policy"]["EnabledFolders"], [ids["Music"], ids["Shows"]],
                         "anna loses the music she saw through the twin")
        x = self.xml.read_text()
        self.assertNotIn(f"<string>{ids['Music2']}</string>", x, "new users get a library that is gone")
        self.assertIn(f"<string>{ids['Music']}</string>", x)
        p = self.run_hook()
        self.assertEqual((p.returncode, len(JF["deleted"])), (0, 5), p.stdout + p.stderr)

    def ready(self, books=140, podcasts=2, radios=8):
        SIDE.update(abs_libs=[{"id": "abs-b", "mediaType": "book", "folders": [{"fullPath": "/audiobooks"}]},
                              {"id": "abs-p", "mediaType": "podcast", "folders": [{"fullPath": "/podcasts"}]}],
                    abs_totals={"abs-b": books, "abs-p": podcasts}, radios=radios)

    def secrets(self):
        return {"LAIB_SECRET_ABS_MA_TOKEN": "abs-key-1", "LAIB_SECRET_HA_MUSIC_ASSISTANT_TOKEN": "ma-tok-1"}

    def test_spoken_word_stays_in_jellyfin_until_the_owner_switches(self):
        self.box()
        self.ready()
        p = self.run_hook(**self.secrets())
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertTrue({"Audiobooks", "Podcasts", "Radio"} <= self.names(), "gone without the owner's switch")
        self.assertIn("SPOKEN_WORD", p.stdout)

    def test_spoken_word_goes_only_once_its_replacement_answers(self):
        ids = self.box()
        self.ready(books=0, podcasts=2, radios=0)            # ABS still scanning the books, MA without radio
        p = self.run_hook(SPOKEN_WORD="audiobookshelf", **self.secrets())
        self.assertNotEqual(p.returncode, 0, "the family's audiobooks would stay in Jellyfin, yet the apply is green")
        self.assertIn("Podcasts", JF["deleted"])
        self.assertTrue({"Audiobooks", "Radio"} <= self.names(), "removed before the replacement holds anything")
        self.assertIn("Audiobooks", p.stderr)
        self.assertIn("Radio", p.stderr)
        self.ready()
        p = self.run_hook(SPOKEN_WORD="audiobookshelf", **self.secrets())
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertFalse({"Audiobooks", "Podcasts", "Radio"} & self.names())
        self.assertTrue({"Music", "Music (cdopp)", "Movies", "Movies (mdopp)", "Shows"} <= self.names(), "music or video went too")
        self.assertNotIn(f"<string>{ids['Audiobooks']}</string>", self.xml.read_text(), "new users get a library that is gone")

    def test_spoken_word_without_audiobookshelf_or_music_assistant_stays(self):
        self.box()
        self.ready()
        p = self.run_hook(SPOKEN_WORD="audiobookshelf")      # neither key handed over yet: ABS not installed
        self.assertNotEqual(p.returncode, 0)
        self.assertTrue({"Audiobooks", "Podcasts", "Radio"} <= self.names())
        self.assertIn("laib add audiobookshelf", p.stderr)


if __name__ == "__main__":
    unittest.main()
