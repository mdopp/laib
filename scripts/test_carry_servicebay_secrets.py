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
                       ("LLDAP", "STORAGE", "IMMICH", "PAPERLESS", "JELLYFIN", "ADGUARD", "SHARE")}
        seal = lambda v: subprocess.run(["node", "-e", SEAL, key.hex(), v], capture_output=True, text=True, check=True).stdout
        cfg = {
            "lldap": {"password": seal(self.values["LLDAP"])},     # legacy field, pre-installedSecrets
            "adguard": {"password": seal("stale-legacy")},          # installedSecrets wins over it
            "installedSecrets": [
                {"varName": "AUTHELIA_STORAGE_ENCRYPTION_KEY", "password": seal(self.values["STORAGE"])},
                {"varName": "DB_PASSWORD", "password": seal(self.values["IMMICH"])},
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
SB_DATA={self.data} exec "$@"
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
        self.assertEqual(set(self.laib_sets(calls)), {"IMMICH_DB_PASSWORD", "SAMBA_PASSWORD"})

    def test_a_wrong_key_sets_nothing(self):
        (self.data / "secret.key").write_bytes(secrets.token_bytes(32))
        p, calls = self.run_script("immich")
        self.assertNotEqual(p.returncode, 0)
        self.assertEqual(self.laib_sets(calls), {})


if __name__ == "__main__":
    unittest.main()
