"""#256: Jellyfin logs the family in with their LLDAP accounts, as on
ServiceBay (templates/media/post-deploy.py:679-800 there): the
LDAP-Authentication plugin, installed and its configuration rewritten on
every apply, users created on their first login, the admins group mapped to
Jellyfin administrators, the local admin kept as break-glass, Quick Connect
on. The plugin binds with a read-only LLDAP account (as radicale does since
#55), never the LLDAP admin as ServiceBay did. A fake Jellyfin answers like
Jellyfin 12, which ignores X-Emby-Authorization (ServiceBay #3048)."""
import json, os, re, subprocess, sys, tempfile, threading, unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

HOOK = Path(__file__).resolve().parent.parent / "post-deploy"
LDAP_PW, ADMIN_PW = "-ldap-ro-pw-8K", "admin-pw-3J"
PLUGIN = "958aad6637844d2ab89aa7b6fab6e25c"
JF = {}


def fresh():
    JF.clear()
    JF.update(wizard=False, users={}, folders=[], plugins=[], pending=False, restarts=0, installs=0, starting=0, wizard503=0,
              config={"QuickConnectAvailable": False, "ServerName": "x"})


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

    def do_GET(self):
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
        if self.path == "/Library/VirtualFolders":
            return self.reply(200, [{"Name": n, "ItemId": i} for n, i in JF["folders"]])
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
            if not u or u["pw"] != b["Pw"]:
                return self.reply(401)
            return self.reply(200, {"AccessToken": "tok"})
        if not self.token():
            return self.reply(401)
        if p.startswith("/Library/VirtualFolders?"):
            name = re.search(r"name=([^&]+)", p).group(1)
            JF["folders"].append((name, f"lib-{len(JF['folders']) + 1}"))
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
            JF["users"][b["Name"]] = {"Name": b["Name"], "Id": "u-" + b["Name"], "pw": b["Password"]}
            return self.reply(200, JF["users"][b["Name"]])
        self.reply(404)


class PostDeploy(unittest.TestCase):
    def setUp(self):
        fresh()
        self.srv = HTTPServer(("127.0.0.1", 0), FakeJellyfin)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.addCleanup(self.srv.shutdown)
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.media = Path(self.tmp.name, "share")
        for d in ("music", "movies"):
            (self.media / d).mkdir(parents=True)
        self.xml = Path(self.tmp.name, "media/jellyfin-config/plugins/configurations/LDAP-Auth.xml")

    def run_hook(self):
        env = dict(os.environ, JELLYFIN_URL=f"http://127.0.0.1:{self.srv.server_port}", LAIB_DATA_DIR=self.tmp.name,
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
        self.assertEqual(set(JF["users"]), {"admin"}, "a shared login was created next to the family's own")
        self.assertNotIn(LDAP_PW, p.stdout + p.stderr)
        p = self.run_hook()
        self.assertEqual((p.returncode, JF["installs"], JF["restarts"]), (0, 1, 1), p.stdout + p.stderr)

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


if __name__ == "__main__":
    unittest.main()
