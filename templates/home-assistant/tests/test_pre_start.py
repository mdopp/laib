"""#253: Home Assistant keeps the family's Authelia login (owner, #229).

Laib's pre-start, before Home Assistant starts, installs
christiaangoossens/hass-oidc-auth from a SHA-256-pinned tag archive and
writes the block as a package Laib owns whole (packages/laib_auth_oidc.yaml,
the secret from the unit's environment, never in a file). The admin's
configuration.yaml is only added to. The archive is served here by a fake
GitHub."""
import hashlib, io, os, re, subprocess, sys, tarfile, tempfile, threading, unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

T = Path(__file__).resolve().parent.parent
HOOK = T / "pre-start"


def archive(marker="1.1.0"):
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tf:
        for name, data in ((f"hass-oidc-auth-1.1.0/custom_components/auth_oidc/manifest.json", '{"domain": "auth_oidc", "version": "%s"}' % marker),
                           ("hass-oidc-auth-1.1.0/custom_components/auth_oidc/__init__.py", "# component\n"),
                           ("hass-oidc-auth-1.1.0/README.md", "not this\n")):
            info = tarfile.TarInfo(name)
            info.size = len(data.encode())
            tf.addfile(info, io.BytesIO(data.encode()))
    return buf.getvalue()


SERVED = {"body": archive(), "hits": 0}


class FakeGitHub(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_GET(self):
        SERVED["hits"] += 1
        self.send_response(200)
        self.send_header("Content-Length", str(len(SERVED["body"])))
        self.end_headers()
        self.wfile.write(SERVED["body"])


# the shape of the Kraftwerk's configuration.yaml today (read-only, 2026-10-24)
KRAFTWERK = """default_config:

automation: !include automations.yaml
script: !include scripts.yaml
scene: !include scenes.yaml

# auth_oidc: moved to packages/laib_auth_oidc.yaml by laib (#253)

homeassistant:
  packages: !include_dir_named packages
"""


class PreStart(unittest.TestCase):
    def setUp(self):
        SERVED.update(body=archive(), hits=0)
        self.srv = HTTPServer(("127.0.0.1", 0), FakeGitHub)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.addCleanup(self.srv.shutdown)
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.c = Path(self.tmp.name) / "home-assistant" / "homeassistant"
        self.c.mkdir(parents=True)

    def run_hook(self, sha=None):
        env = dict(os.environ, LAIB_DATA_DIR=self.tmp.name, LAIB_DOMAIN="vm.test", OIDC_SECRET="laib-oidc-secret-Q9",
                   AUTH_OIDC_ARCHIVE=f"http://127.0.0.1:{self.srv.server_port}/v1.1.0.tar.gz",
                   AUTH_OIDC_SHA256=sha or hashlib.sha256(SERVED["body"]).hexdigest())
        return subprocess.run([sys.executable, str(HOOK)], env=env, capture_output=True, text=True, timeout=60)

    def everything(self):
        return "".join(p.read_text(errors="replace") for p in self.c.rglob("*") if p.is_file())

    def test_a_fresh_home_assistant_starts_with_the_family_login(self):
        p = self.run_hook()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertTrue((self.c / "custom_components/auth_oidc/manifest.json").exists())
        self.assertFalse((self.c / "custom_components/README.md").exists(), "more than the component was unpacked")
        pkg = (self.c / "packages/laib_auth_oidc.yaml").read_text()
        for want in ("auth_oidc:", "client_id: homeassistant", "client_secret: !env_var AUTH_OIDC_SECRET",
                     "discovery_url: https://auth.vm.test/.well-known/openid-configuration",
                     "automatic_user_linking: true", "automatic_person_creation: true", "admin: admins", "user: family"):
            self.assertIn(want, pkg)
        conf = (self.c / "configuration.yaml").read_text()
        self.assertIn("default_config:", conf)
        self.assertRegex(conf, r"(?m)^homeassistant:\n  packages: !include_dir_named packages$")
        for f in ("automations.yaml", "scripts.yaml", "scenes.yaml", "packages/laib_mitteilung.yaml"):
            self.assertTrue((self.c / f).exists(), f)
        self.assertNotIn("laib-oidc-secret-Q9", self.everything(), "the OIDC secret is in a file; it belongs in the unit's environment")
        hits = SERVED["hits"]
        before = {p: p.read_bytes() for p in self.c.rglob("*") if p.is_file()}
        p = self.run_hook()
        self.assertEqual((p.returncode, SERVED["hits"]), (0, hits), "the pinned archive was fetched again")
        self.assertEqual(before, {p: p.read_bytes() for p in self.c.rglob("*") if p.is_file()}, "a second run changed files")

    def test_the_admins_configuration_is_left_as_it_is(self):
        (self.c / "configuration.yaml").write_text(KRAFTWERK)
        (self.c / ".storage").mkdir()
        (self.c / ".storage/auth_provider.auth_oidc").write_text('{"linked": "anna"}')
        p = self.run_hook()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertEqual((self.c / "configuration.yaml").read_text(), KRAFTWERK)
        self.assertEqual((self.c / ".storage/auth_provider.auth_oidc").read_text(), '{"linked": "anna"}', "the linked users were touched")
        self.assertTrue((self.c / "custom_components/auth_oidc/manifest.json").exists())
        self.assertIn("client_id: homeassistant", (self.c / "packages/laib_auth_oidc.yaml").read_text())

    def test_an_archive_that_is_not_the_pinned_one_is_refused_and_nothing_replaced(self):
        good = self.run_hook()
        self.assertEqual(good.returncode, 0, good.stderr)
        pinned = hashlib.sha256(SERVED["body"]).hexdigest()
        SERVED["body"] = archive("evil")
        (self.c / "custom_components/auth_oidc/.laib-archive").unlink()   # a reinstall is due
        p = self.run_hook(sha=pinned)
        self.assertNotEqual(p.returncode, 0, "a changed archive was accepted")
        self.assertIn("SHA-256", p.stderr)
        self.assertIn('"1.1.0"', (self.c / "custom_components/auth_oidc/manifest.json").read_text())

    def test_the_unit_and_the_contract(self):
        unit = (T / "home-assistant.container").read_text()
        self.assertIn("Environment=AUTH_OIDC_SECRET=${OIDC_SECRET}", unit)
        contract = (T / "laib.yml").read_text()
        self.assertRegex(contract, r"(?s)oidc:[^\n]*\n\s+clientId: homeassistant.*/auth/oidc/callback.*tokenEndpointAuthMethod: client_secret_post")
        self.assertIn("OIDC_SECRET: !secret OIDC_HOMEASSISTANT_SECRET", (T / "values.example.yml").read_text())
        # the config is pre-start's: it must be there before Home Assistant starts; post-deploy only mints the tokens (#349)
        hook = (T / "post-deploy").read_text()
        self.assertFalse(any(w in hook for w in ("configuration.yaml", "packages/", "custom_components")), "post-deploy writes config")


if __name__ == "__main__":
    unittest.main()
