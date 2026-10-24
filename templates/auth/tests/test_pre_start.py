"""auth pre-start (#36): Authelia's configuration.yml, the signing key and
the OIDC clients. Authelia replaces a list from a later config file instead
of merging it (koanf's Merge), so the clients Laib renders are one list, in
one file pre-start writes (oidc-clients.yml). The ServiceBay carry-over
(#245) is gone: a configuration.yml from before is overwritten, its clients
are not kept."""
import json, os, re, subprocess, sys, tempfile, unittest
from pathlib import Path

HOOK = Path(__file__).resolve().parent.parent / "pre-start"
UNIT = Path(__file__).resolve().parent.parent / "auth-authelia.container"
SECRETS = {n: "s-" + n.lower() for n in ("AUTHELIA_JWT_SECRET", "AUTHELIA_SESSION_SECRET", "AUTHELIA_STORAGE_KEY",
                                         "AUTHELIA_OIDC_HMAC_SECRET", "LLDAP_ADMIN_PASSWORD")}
KEY = "-----BEGIN PRIVATE KEY-----\nMIIsyntheticLAIBkey\nAAAA\n-----END PRIVATE KEY-----"
# a configuration.yml not written by this hook, with a client of its own; every value synthetic
FOREIGN = """theme: dark
identity_providers:
  oidc:
    clients:
      - client_id: oldapp
        client_secret: '$plaintext$old-secret'
"""


def laib_clients(*ids):
    if not ids:
        return "# Rendered by laib: one OIDC client per service that declares oidc.\n# no service declares oidc yet\n"
    return "# Rendered by laib: one OIDC client per service that declares oidc.\nidentity_providers:\n  oidc:\n    clients:\n" + "".join(
        f"      - client_id: {i}\n        client_name: {i}\n        client_secret: \"$pbkdf2-sha512$310000$laib-{i}\"\n"
        f"        authorization_policy: one_factor\n        redirect_uris:\n          - https://{i}.vm.test/cb\n        scopes: [openid]\n" for i in ids)


def ids(text):
    return re.findall(r"(?m)^      - client_id: '?([^'\s]+)'?$", text)


class PreStart(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        d = Path(self.tmp.name)
        self.cfg, self.render = d / "auth" / "authelia-config", d / "render"
        self.cfg.mkdir(parents=True)
        (self.render / "authelia").mkdir(parents=True)
        self.env = dict(os.environ, LAIB_DATA_DIR=str(d), LAIB_DOMAIN="vm.test", LAIB_RENDER_DIR=str(self.render),
                        BASE_DN="dc=vm,dc=test", LAIB_START_URL="https://admin.vm.test/", **{"LAIB_SECRET_" + k: v for k, v in SECRETS.items()})

    def run_hook(self, *laib_ids):
        (self.render / "authelia" / "laib-clients.yml").write_text(laib_clients(*laib_ids))
        p = subprocess.run([sys.executable, str(HOOK)], env=self.env, capture_output=True, text=True, timeout=120)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        return p, (self.cfg / "oidc-clients.yml").read_text(), (self.cfg / "configuration.yml").read_text()

    def test_a_foreign_config_is_overwritten_and_its_clients_are_not_kept(self):
        (self.cfg / "oidc.pem").write_text(KEY)
        (self.cfg / "configuration.yml").write_text(FOREIGN)
        p, clients, conf = self.run_hook("immich", "vaultwarden")
        self.assertEqual(sorted(ids(clients)), ["immich", "vaultwarden"])
        self.assertNotIn("old-secret", clients + conf + p.stdout + p.stderr)
        self.assertIn("rendered by laib pre-start", conf)
        self.assertEqual((self.cfg / "oidc.pem").read_text().strip(), KEY, "the signing key is kept")
        self.assertEqual(sorted(f.name for f in self.cfg.iterdir() if f.is_file()),
                         ["configuration.yml", "laib-status.json", "oidc-clients.yml", "oidc.pem"])
        # the next apply changes nothing
        _, again, conf2 = self.run_hook("immich", "vaultwarden")
        self.assertEqual((again, conf2), (clients, conf))

    def test_a_fresh_box_carries_nothing(self):
        (self.cfg / "oidc.pem").write_text(KEY)   # skips the 4096-bit key generation
        _, clients, conf = self.run_hook()
        self.assertEqual(ids(clients), [])
        self.assertNotIn("identity_providers", clients + conf, "Authelia refuses an empty oidc block (#36)")
        _, clients, conf = self.run_hook("immich")
        self.assertEqual(ids(clients), ["immich"])
        self.assertIn("identity_providers:", conf)

    def notifier(self, **smtp):
        (self.cfg / "oidc.pem").write_text(KEY)
        self.env.update(smtp)
        p, _, conf = self.run_hook()
        status = json.loads((self.cfg / "laib-status.json").read_text())
        return p, conf.split("notifier:\n", 1)[1].split("\n#", 1)[0], status

    def test_no_mail_server_is_a_file_and_a_yellow_finding(self):
        # #359: a fresh box without SMTP still starts; diag and the Box page say resets are not mailed
        _, notifier, status = self.notifier()
        self.assertIn("filesystem:", notifier)
        self.assertNotIn("smtp", notifier)
        self.assertEqual(status["detail"], "Passwort-Mails gehen nicht (kein Mailserver)")
        self.assertEqual([f["severity"] for f in status["findings"]], ["gelb"])
        self.assertIn("SMTP_HOST", status["findings"][0]["fix"])
        self.assertIn("at", status)

    def test_mail_server_renders_smtp_with_the_password_from_the_podman_secret(self):
        pw = "pw-' {{ x }}\"gmail"
        p, notifier, status = self.notifier(SMTP_HOST="smtp.gmail.com", SMTP_PORT="587", SMTP_SECURITY="starttls",
                                            SMTP_USER="box@gmail.com", SMTP_FROM="Die Box <box@gmail.com>", SMTP_PASSWORD=pw)
        self.assertIn("address: 'submission://smtp.gmail.com:587'", notifier)
        self.assertIn("sender: 'Die Box <box@gmail.com>'", notifier)
        self.assertIn("username: 'box@gmail.com'", notifier)
        self.assertIn('password: |2-\n      {{ env "X_LAIB_SMTP_PASSWORD" }}', notifier)
        self.assertIn("disable_startup_check: true", notifier, "a mail server that is down must not keep everyone out (#987)")
        self.assertNotIn("filesystem", notifier)
        self.assertNotIn("disable_require_tls", notifier)
        self.assertNotIn(pw, (self.cfg / "configuration.yml").read_text() + p.stdout + p.stderr)
        self.assertEqual(status, {"detail": "Passwort-Mails über smtp.gmail.com", "findings": [], "at": status["at"]})
        self.assertIn("Environment=X_LAIB_SMTP_PASSWORD=${SMTP_PASSWORD}", UNIT.read_text())

    def test_implicit_tls_and_a_relay_without_tls_or_login(self):
        _, notifier, _ = self.notifier(SMTP_HOST="mail.test", SMTP_PORT="465", SMTP_SECURITY="tls", SMTP_USER="u@mail.test")
        self.assertIn("filesystem:", notifier, "a login without its password is no mail server")
        _, notifier, _ = self.notifier(SMTP_PASSWORD="x")
        self.assertIn("address: 'submissions://mail.test:465'", notifier)
        _, notifier, _ = self.notifier(SMTP_HOST="host.containers.internal", SMTP_PORT="1025", SMTP_SECURITY="none", SMTP_USER="", SMTP_FROM="box@mail.test")
        self.assertIn("address: 'smtp://host.containers.internal:1025'", notifier)
        self.assertIn("disable_require_tls: true", notifier)
        self.assertNotIn("username", notifier)
        self.assertNotIn("password", notifier)

    def test_an_incomplete_setting_says_what_is_missing(self):
        _, notifier, status = self.notifier(SMTP_HOST="mail.test", SMTP_SECURITY="ssl")
        self.assertIn("filesystem:", notifier)
        self.assertIn("SMTP_FROM", status["findings"][0]["detail"])
        self.assertIn("SMTP_SECURITY", status["findings"][0]["detail"])

    def test_a_login_without_rd_lands_on_the_start_url(self):
        # #381: box.yml laib.host admin moved the box page; the default redirect followed laib.<domain>
        (self.cfg / "oidc.pem").write_text(KEY)
        _, _, conf = self.run_hook()
        self.assertIn("default_redirection_url: 'https://admin.vm.test/'", conf)
        self.assertNotIn("laib.vm.test", conf)

    def test_authelia_reads_the_one_list(self):
        unit = UNIT.read_text()
        self.assertIn("/config/oidc-clients.yml", unit)
        self.assertNotIn("/laib/laib-clients.yml", unit, "a second clients list would replace the first (koanf)")


if __name__ == "__main__":
    unittest.main()
