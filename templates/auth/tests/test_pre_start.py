"""#245: when Laib's auth takes over from ServiceBay's, the OIDC clients of
the apps that have not moved yet must not vanish. ServiceBay's Authelia kept
them in authelia-config/configuration.yml, the very file pre-start writes.
pre-start keeps them once (servicebay-clients.yml) and registers each one
until a Laib service declares the same client id; the signing key stays, so
the JWKS and the tokens the apps hold stay valid.

Authelia replaces a list from a later config file instead of merging it
(koanf's Merge), so Laib's clients and the carried ones are one list, in one
file pre-start writes (oidc-clients.yml)."""
import os, re, subprocess, tempfile, unittest
from pathlib import Path

HOOK = Path(__file__).resolve().parent.parent / "pre-start"
UNIT = Path(__file__).resolve().parent.parent / "auth-authelia.container"
SECRETS = {n: "s-" + n.lower() for n in ("AUTHELIA_JWT_SECRET", "AUTHELIA_SESSION_SECRET", "AUTHELIA_STORAGE_KEY",
                                         "AUTHELIA_OIDC_HMAC_SECRET", "LLDAP_ADMIN_PASSWORD")}
KEY = "-----BEGIN PRIVATE KEY-----\nMIIsyntheticSERVICEBAYkey\nAAAA\n-----END PRIVATE KEY-----"
# the shape of the Kraftwerk's file (read-only, 2026-10-16); every value synthetic
SERVICEBAY = """theme: dark
server:
  address: tcp://0.0.0.0:9091
access_control:
  default_policy: deny
  rules:
    - domain: '*.vm.test'
      policy: one_factor
identity_providers:
  oidc:
    hmac_secret: sb-hmac
    jwks:
      - key_id: main
        algorithm: RS256
        use: sig
        key: |
""" + "".join("          " + l + "\n" for l in KEY.splitlines()) + """    clients:
      - client_id: servicebay
        client_name: ServiceBay
        client_secret: '$plaintext$sb-admin-secret'
        authorization_policy: two_factor
        redirect_uris:
          - https://admin.vm.test/api/auth/oidc/callback
        token_endpoint_auth_method: client_secret_post
      - client_id: immich
        client_name: Immich
        client_secret: '$plaintext$sb-immich-secret'
        authorization_policy: one_factor
        redirect_uris:
          - https://photos.vm.test/auth/login
          - app.immich:/
      - client_id: homeassistant
        client_name: Home Assistant
        client_secret: '$plaintext$sb-ha-secret'
        authorization_policy: one_factor
        redirect_uris:
          - https://home.vm.test/auth/oidc/callback
        token_endpoint_auth_method: client_secret_post
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
                        BASE_DN="dc=vm,dc=test", **{"LAIB_SECRET_" + k: v for k, v in SECRETS.items()})

    def run_hook(self, *laib_ids):
        (self.render / "authelia" / "laib-clients.yml").write_text(laib_clients(*laib_ids))
        p = subprocess.run([str(HOOK)], env=self.env, capture_output=True, text=True, timeout=120)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        return p, (self.cfg / "oidc-clients.yml").read_text(), (self.cfg / "configuration.yml").read_text()

    def test_servicebays_clients_stay_until_their_service_moves(self):
        (self.cfg / "configuration.yml").write_text(SERVICEBAY)
        p, clients, conf = self.run_hook("vaultwarden", "immich")
        self.assertEqual(sorted(ids(clients)), ["homeassistant", "immich", "servicebay", "vaultwarden"])
        self.assertIn("$pbkdf2-sha512$310000$laib-immich", clients, "Laib's immich client must win over ServiceBay's")
        self.assertNotIn("sb-immich-secret", clients)
        self.assertIn("'$plaintext$sb-ha-secret'", clients, "ServiceBay's homeassistant client was not carried as it was")
        self.assertIn("token_endpoint_auth_method: client_secret_post", clients)
        self.assertIn("homeassistant", p.stdout, "the hook does not say which clients are still ServiceBay's")
        self.assertIn("rendered by laib pre-start", conf)
        self.assertIn("identity_providers:", conf)
        self.assertEqual((self.cfg / "oidc.pem").read_text().strip(), KEY, "ServiceBay's signing key was not kept")
        for s in ("sb-ha-secret", "sb-admin-secret"):
            self.assertNotIn(s, p.stdout + p.stderr)
        # the next apply: configuration.yml is Laib's now; home-assistant has moved
        p, clients, _ = self.run_hook("vaultwarden", "immich", "homeassistant")
        self.assertEqual(sorted(ids(clients)), ["homeassistant", "immich", "servicebay", "vaultwarden"])
        self.assertNotIn("sb-ha-secret", clients, "a moved service still has ServiceBay's client")
        # every service has moved but ServiceBay's own login (until step 7)
        _, clients, _ = self.run_hook("vaultwarden", "immich", "homeassistant", "servicebay")
        self.assertNotIn("$plaintext$", clients)

    def test_carried_clients_alone_open_the_oidc_block(self):
        (self.cfg / "configuration.yml").write_text(SERVICEBAY)
        _, clients, conf = self.run_hook()
        self.assertEqual(sorted(ids(clients)), ["homeassistant", "immich", "servicebay"])
        self.assertIn("identity_providers:", conf, "Authelia gets clients but no hmac/jwks")

    def test_a_fresh_box_carries_nothing(self):
        (self.cfg / "oidc.pem").write_text(KEY)   # skips the 4096-bit key generation
        _, clients, conf = self.run_hook()
        self.assertEqual(ids(clients), [])
        self.assertNotIn("identity_providers", clients + conf, "Authelia refuses an empty oidc block (#36)")
        self.assertFalse((self.cfg / "servicebay-clients.yml").exists())
        _, clients, conf = self.run_hook("immich")
        self.assertEqual(ids(clients), ["immich"])
        self.assertIn("identity_providers:", conf)

    def test_authelia_reads_the_one_list(self):
        unit = UNIT.read_text()
        self.assertIn("/config/oidc-clients.yml", unit)
        self.assertNotIn("/laib/laib-clients.yml", unit, "a second clients list would replace the first (koanf)")


if __name__ == "__main__":
    unittest.main()
