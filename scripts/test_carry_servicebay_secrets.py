"""#228: scripts/carry-servicebay-secrets.sh against a synthetic ServiceBay
store: a made-up secret.key, values sealed the way ServiceBay seals them
(enc:v1:iv:tag:ct, AES-256-GCM, by node as ServiceBay does), one in the legacy
`lldap` field, one stored plain, one missing.

`podman` is a shim that runs the in-container program with local node against
the fixture; `laib` is a shim that records its argv and stdin. The script is
run the way the operator runs it, as a process."""
import json, os, secrets, shutil, stat, subprocess, tempfile, unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
SCRIPT = HERE / "carry-servicebay-secrets.sh"

SEAL = """const c=require('crypto'),k=Buffer.from(process.argv[1],'hex'),iv=c.randomBytes(16),x=c.createCipheriv('aes-256-gcm',k,iv);
const ct=x.update(process.argv[2],'utf8','hex')+x.final('hex');process.stdout.write('enc:v1:'+iv.toString('hex')+':'+x.getAuthTag().toString('hex')+':'+ct)"""


def exe(path, body):
    path.write_text(body)
    path.chmod(path.stat().st_mode | stat.S_IEXEC)


class Carry(unittest.TestCase):
    def setUp(self):
        if shutil.which("node") is None:
            self.fail("node is needed: ServiceBay's container decrypts with it, and so does this test")
        self.tmp = tempfile.TemporaryDirectory()
        t = Path(self.tmp.name)
        self.data, self.bin, self.log = t / "data", t / "bin", t / "calls"
        self.data.mkdir()
        self.bin.mkdir()
        key = secrets.token_bytes(32)
        (self.data / "secret.key").write_bytes(key)
        # synthetic values, one per shape; none is anything real
        self.values = {name: "v-" + secrets.token_hex(8) for name in
                       ("LLDAP", "STORAGE", "IMMICH", "IMMICH_ADMIN", "PAPERLESS", "JELLYFIN", "ADGUARD", "SHARE")}
        seal = lambda v: subprocess.run(["node", "-e", SEAL, key.hex(), v], capture_output=True, text=True, check=True).stdout
        cfg = {
            "lldap": {"password": seal(self.values["LLDAP"])},     # legacy field, pre-installedSecrets
            "adguard": {"password": seal("stale-legacy")},          # installedSecrets wins over it
            "installedSecrets": [
                {"varName": "AUTHELIA_STORAGE_ENCRYPTION_KEY", "password": seal(self.values["STORAGE"])},
                {"varName": "DB_PASSWORD", "password": seal(self.values["IMMICH"])},
                {"varName": "IMMICH_ADMIN_PASSWORD", "password": seal(self.values["IMMICH_ADMIN"])},
                {"varName": "PAPERLESS_DB_PASSWORD", "password": seal(self.values["PAPERLESS"])},
                {"varName": "JELLYFIN_ADMIN_PASSWORD", "password": seal(self.values["JELLYFIN"])},
                {"varName": "ADGUARD_ADMIN_PASSWORD", "password": seal(self.values["ADGUARD"])},
                {"varName": "SHARE_PASSWORD", "password": self.values["SHARE"]},   # stored plain
                # MQTT_PASSWORD missing: reported, not set
            ],
        }
        (self.data / "config.json").write_text(json.dumps(cfg))
        # podman exec <container> node -e <prog> <name>: run it here, against the fixture
        exe(self.bin / "podman", f"""#!/bin/sh
printf 'podman %s\\n' "$*" >> {self.log}
[ "$1" = exec ] || exit 9
shift 2
DATA_DIR={self.data} exec "$@"
""")
        exe(self.bin / "laib", f"""#!/bin/sh
read -r v
printf 'laib %s <%s>\\n' "$*" "$v" >> {self.log}
[ -n "$v" ] || {{ echo "empty value on stdin" >&2; exit 1; }}
""")

    def tearDown(self):
        self.tmp.cleanup()

    def run_script(self, *args):
        env = dict(os.environ, PATH=f"{self.bin}:{os.environ['PATH']}")
        p = subprocess.run(["bash", str(SCRIPT), *args], capture_output=True, text=True, env=env)
        calls = self.log.read_text().splitlines() if self.log.exists() else []
        return p, calls

    def laib_sets(self, calls):
        out = {}
        for c in calls:
            if c.startswith("laib secret set "):
                name, _, rest = c[len("laib secret set "):].partition(" <")
                if rest[:-1]:      # laib refuses an empty stdin and sets nothing
                    out[name] = rest[:-1]
        return out

    def test_every_value_goes_under_laibs_name_on_stdin(self):
        p, calls = self.run_script()
        sets = self.laib_sets(calls)
        self.assertEqual(sets, {
            "LLDAP_ADMIN_PASSWORD": self.values["LLDAP"],
            "AUTHELIA_STORAGE_KEY": self.values["STORAGE"],
            "IMMICH_DB_PASSWORD": self.values["IMMICH"],
            "IMMICH_ADMIN_PASSWORD": self.values["IMMICH_ADMIN"],
            "PAPERLESS_DB_PASSWORD": self.values["PAPERLESS"],
            "JELLYFIN_ADMIN_PASSWORD": self.values["JELLYFIN"],
            "ADGUARD_ADMIN_PASSWORD": self.values["ADGUARD"],
            "SAMBA_PASSWORD": self.values["SHARE"],
        })
        self.assertTrue(all(c.split()[2] == "servicebay" for c in calls if c.startswith("podman ")), calls)

    def test_a_missing_value_is_reported_and_fails_the_run(self):
        p, calls = self.run_script()
        self.assertNotEqual(p.returncode, 0)
        self.assertIn("MQTT_PASSWORD", p.stderr)
        self.assertNotIn("MQTT_PASSWORD", self.laib_sets(calls))

    def test_no_value_on_argv_or_the_terminal(self):
        p, calls = self.run_script()
        argv = [c.split(" <")[0] if c.startswith("laib ") else c for c in calls]
        for v in list(self.values.values()) + ["stale-legacy"]:
            for line in argv:
                self.assertNotIn(v, line)
            self.assertNotIn(v, p.stdout + p.stderr)

    def test_only_the_named_services(self):
        p, calls = self.run_script("immich", "file-share")
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertEqual(set(self.laib_sets(calls)), {"IMMICH_DB_PASSWORD", "IMMICH_ADMIN_PASSWORD", "SAMBA_PASSWORD"})

    def test_a_wrong_key_sets_nothing(self):
        (self.data / "secret.key").write_bytes(secrets.token_bytes(32))
        p, calls = self.run_script("immich")
        self.assertNotEqual(p.returncode, 0)
        self.assertEqual(self.laib_sets(calls), {})

    def test_a_redaction_mask_is_no_value(self):
        # ServiceBay's two masks (#1275 __REDACTED__, #2296 <redacted>) once
        # landed in its store as values; carried, they become the password
        cfg = json.loads((self.data / "config.json").read_text())
        key = (self.data / "secret.key").read_bytes()
        seal = lambda v: subprocess.run(["node", "-e", SEAL, key.hex(), v], capture_output=True, text=True, check=True).stdout
        cfg["installedSecrets"] += [{"varName": "MQTT_PASSWORD", "password": seal("<redacted>")},
                                    {"varName": "JELLYFIN_ADMIN_PASSWORD", "password": "__REDACTED__"}]
        cfg["installedSecrets"] = [e for e in cfg["installedSecrets"] if e["varName"] != "JELLYFIN_ADMIN_PASSWORD" or e["password"] == "__REDACTED__"]
        (self.data / "config.json").write_text(json.dumps(cfg))
        p, calls = self.run_script("mosquitto", "media")
        self.assertNotEqual(p.returncode, 0)
        self.assertEqual(self.laib_sets(calls), {})
        self.assertIn("MQTT_PASSWORD", p.stderr)
        self.assertIn("JELLYFIN_ADMIN_PASSWORD", p.stderr)

    def test_a_torn_config_shows_no_value(self):
        # node's JSON.parse error quotes the line it failed on: config.json
        # holds plain values too (a legacy field, auth.passwordHash)
        (self.data / "config.json").write_text('{"installedSecrets":[{"varName":"DB_PASSWORD","password":"plain-marker-4711"}')
        p, calls = self.run_script("immich")
        self.assertNotEqual(p.returncode, 0)
        self.assertIn("DB_PASSWORD", p.stderr)
        self.assertNotIn("plain-marker-4711", p.stdout + p.stderr)
        self.assertEqual([l for l in p.stderr.splitlines() if not l.startswith("immich: ")], [], "only the script's own report")
        self.assertEqual(self.laib_sets(calls), {})

    def test_an_unknown_service_is_refused(self):
        # a typo must not end green with nothing carried: laib add would then
        # generate a fresh value and lock the app out of its data
        p, calls = self.run_script("imich")
        self.assertNotEqual(p.returncode, 0)
        self.assertIn("imich", p.stderr)
        self.assertEqual(self.laib_sets(calls), {})


class ServiceBaysOwnSeal(unittest.TestCase):
    """A store written by ServiceBay's own code, not by this test: its
    persistSingleSecret and updateConfig (secrets.ts encrypt through config.ts)
    into an empty DATA_DIR, with a key it generated. Every value is synthetic.
    The layout this pins: enc:v1:<16-byte IV hex>:<16-byte GCM tag hex>:<ct hex>,
    the raw 32-byte secret.key, utf8 plaintext."""
    KEY = "c46e6cbad34428ad1b56666bde9390703f997805694cea84e133b04f49dc6628"
    CONFIG = {"lldap": {"password": "enc:v1:38b9bf4bbb94a4af33757c584f6b8fbf:a4b7f7e3cccf372ae8a8bd1db906cd5b:aa50af0ee97ebd3c0e0d0be949226cb3e1f30a527043"},
              "installedSecrets": [
                  {"varName": "DB_PASSWORD", "password": "enc:v1:a53a0857ecf8eeef72bc57bd874b3567:d79a13ea60aeec47bafd0ab32b2c86e9:a8da7e3e1aa45f305b5cdb0e345bc96ab8874f2e98dc4a4069bc80f926510120666e839e892c"},
                  {"varName": "AUTHELIA_STORAGE_ENCRYPTION_KEY", "password": "enc:v1:1db58f9f19fde79c7a655566a65d4e97:31412d44fc77af96f460b908a9fea568:f0aaa96b8ce3b90d7e12f4ad8b5bef22e254db1972c018492b"}]}

    def test_values_sealed_by_servicebay_arrive_byte_for_byte(self):
        c = Carry()
        c.setUp()
        try:
            (c.data / "secret.key").write_bytes(bytes.fromhex(self.KEY))
            (c.data / "config.json").write_text(json.dumps(self.CONFIG))
            # the laib shim keeps only what read -r leaves; record stdin raw
            exe(c.bin / "laib", f"#!/bin/sh\ncat > {c.log}.$3\n")
            p, _ = c.run_script("auth", "immich")
            # this store predates IMMICH_ADMIN_PASSWORD in the table (#254): that one is reported, the rest arrive
            self.assertEqual((p.returncode, [l for l in p.stderr.splitlines() if "NOT set" in l]),
                             (1, ["immich: IMMICH_ADMIN_PASSWORD is not in ServiceBay's store; IMMICH_ADMIN_PASSWORD NOT set"]), p.stderr)
            got = {n: Path(f"{c.log}.{n}").read_bytes() for n in ("LLDAP_ADMIN_PASSWORD", "AUTHELIA_STORAGE_KEY", "IMMICH_DB_PASSWORD")}
            self.assertEqual(got, {
                "LLDAP_ADMIN_PASSWORD": b"synthetic-legacy-lldap\n",
                "AUTHELIA_STORAGE_KEY": "synthetic-üñí-ключ\n".encode(),
                "IMMICH_DB_PASSWORD": b" synthetic immich: pw, trailing space \n",
            })
        finally:
            c.tearDown()


if __name__ == "__main__":
    unittest.main()
