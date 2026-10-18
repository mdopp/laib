"""#221: mosquitto's hooks, run the way the box runs them.

Laib runs a hook in the hooks image (hooks/Containerfile: python, bash, curl,
openssl; no podman), with the service's values and LAIB_SECRET_<NAME> in its
environment and its data paths mounted at their host paths
(internal/apply/apply.go runHook). pre-start runs before the unit starts;
post-deploy after a green verify, and also on an apply that changed nothing
(then without pre-start). A `laib-exec: <container> <cmd…>` line is run by
Laib as `podman exec <container> <cmd…>` once the hook has exited, in the
order printed; its argv shows in ps to every user on the box (#214).

Here the hooks image is this process's PATH with a podman that is not there,
and the broker is a fake container: /mosquitto/config is the config data
path, mosquitto_passwd -U hashes as the real one does (a line that is already
a hash gets hashed again, and that login is broken), SIGHUP to PID 1 reloads
the password file."""
import base64, hashlib, os, stat, subprocess, tempfile, unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent
USER, PW = "homeassistant", "-mq_pw-Zz9"   # generated secrets are base64url: one in 64 starts with "-"
CONTAINER = "systemd-mosquitto"            # mosquitto.container → mosquitto.service


def fake_hash(pw):
    return "$7$101$c2FsdA==$" + base64.b64encode(hashlib.sha512(pw.encode()).digest()).decode()


class Box:
    def __init__(self, root):
        self.root = Path(root)
        self.data = self.root / "data"
        self.cfg = self.data / "mosquitto" / "config"
        self.bin = self.root / "bin"
        self.bin.mkdir()
        (self.bin / "podman").write_text("#!/bin/sh\necho 'podman: command not found (the hooks image has none)' >&2\nexit 127\n")
        (self.bin / "podman").chmod(0o755)
        # the hooks image runs as root over the broker's uids; here chown is recorded
        (self.bin / "chown").write_text(f"#!/bin/sh\necho \"$@\" >> {self.root / 'chown.log'}\n")
        (self.bin / "chown").chmod(0o755)
        self.broker = None      # the users the running broker knows: user → hash
        self.execs = []         # every argv laib handed to podman exec
        self.events = []
        for d in ("config", "data"):   # laib creates the declared data paths before any hook
            (self.data / "mosquitto" / d).mkdir(parents=True, exist_ok=True)

    def hook(self, phase):
        path = HERE / phase
        if not path.exists():
            return ""
        env = dict(os.environ, PATH=f"{self.bin}:{os.environ['PATH']}", LAIB_SERVICE="mosquitto", LAIB_PHASE=phase,
                   LAIB_DATA_DIR=str(self.data), LAIB_DOMAIN="vm.test", IMAGE="docker.io/library/eclipse-mosquitto:2",
                   MQTT_USER=USER, MQTT_PW=PW, LAIB_SECRET_MQTT_PASSWORD=PW)
        p = subprocess.run(["bash", str(path)], env=env, cwd=HERE, capture_output=True, text=True, timeout=60)
        if p.returncode != 0:
            raise AssertionError(f"{phase} exited {p.returncode} in the hooks image:\n{p.stdout}{p.stderr}")
        for line in p.stdout.splitlines():
            if line.startswith("laib-exec: "):
                self.laib_exec(line[len("laib-exec: "):].split())
        return p.stdout

    def start(self):
        conf = self.cfg / "mosquitto.conf"
        if not conf.exists():
            raise AssertionError("mosquitto would not start: /mosquitto/config/mosquitto.conf is missing")
        if "password_file /mosquitto/config/passwd" in conf.read_text() and not (self.cfg / "passwd").exists():
            raise AssertionError("mosquitto would not start: its password_file is missing")
        self.load()

    def load(self):
        self.broker = {}
        for line in (self.cfg / "passwd").read_text().splitlines():
            u, _, h = line.partition(":")
            if h.startswith("$7$"):   # mosquitto drops an entry that is no hash
                self.broker[u] = h

    def inside(self, p):
        if not p.startswith("/mosquitto/config/"):
            raise AssertionError(f"not a path in the mosquitto container's config: {p}")
        return self.cfg / p[len("/mosquitto/config/"):]

    def laib_exec(self, argv):
        if argv[0] != CONTAINER:
            raise AssertionError(f"laib refuses laib-exec into {argv[0]}: not one of mosquitto's containers")
        ps = ["podman", "exec"] + argv
        if any(PW in w for w in ps):
            raise AssertionError(f"MQTT_PASSWORD is on a process's argv, where ps shows it: {ps}")
        self.execs.append(argv[1:])
        cmd = argv[1:]
        if cmd[:2] == ["mosquitto_passwd", "-U"] and len(cmd) == 3:
            f = self.inside(cmd[2])
            out = []
            for line in f.read_text().splitlines():
                u, _, pw = line.partition(":")
                out.append(f"{u}:{fake_hash(pw)}")   # the real tool hashes what it finds, a hash too
            f.write_text("".join(l + "\n" for l in out))
            self.events.append("hashed")
        elif cmd[0] == "chown" and len(cmd) == 3:
            self.inside(cmd[2])
            self.events.append("chown " + cmd[1])
        elif cmd == ["kill", "-HUP", "1"]:
            self.load()
            self.events.append("reloaded")
        else:
            raise AssertionError(f"the fake broker does not know {cmd}")

    def apply(self, first):
        """an apply that changed the service (pre-start, start, verify, post-deploy) or one that did not (post-deploy)"""
        if first:
            self.hook("pre-start")
            self.start()
        return self.hook("post-deploy")


class Hooks(unittest.TestCase):
    def setUp(self):
        self.box = Box(tempfile.mkdtemp())

    def assert_broker_takes_the_password(self):
        b = self.box
        self.assertEqual(b.broker.get(USER), fake_hash(PW), f"the broker does not know {USER} with MQTT_PASSWORD: {b.broker} ({b.events})")
        self.assertTrue("hashed" in b.events and b.events[-1] == "reloaded", f"the broker must reload after the file is hashed: {b.events}")
        for f in b.cfg.rglob("*"):
            if f.is_file():
                self.assertNotIn(PW, f.read_text(), f"{f.name} keeps the password in plain text")
        self.assertEqual(stat.S_IMODE((b.cfg / "passwd").stat().st_mode), 0o600)

    def test_first_deploy_hashes_the_password_file_and_the_broker_takes_it(self):
        out = self.box.apply(first=True)
        self.assertNotIn(PW, out)
        self.assert_broker_takes_the_password()

    def test_an_apply_without_pre_start_does_not_hash_the_hash(self):
        self.box.apply(first=True)
        self.box.events.clear()
        self.box.apply(first=False)   # nothing changed: post-deploy only
        self.assert_broker_takes_the_password()


if __name__ == "__main__":
    unittest.main()


class Owner(unittest.TestCase):
    """#248: eclipse-mosquitto 2.1 drops to its own user (1883) before it opens
    password_file; a 0600 file of the hooks image's root (the box's user) is
    one it cannot open, so the broker exited 13 on its first start and the
    post-deploy found no container to hash in (the Generalprobe's mosquitto)."""

    def test_pre_start_hands_the_password_file_to_the_brokers_user(self):
        with tempfile.TemporaryDirectory() as d:
            box = Box(d)
            log = Path(d) / "chown.log"
            box.hook("pre-start")
            calls = log.read_text() if log.exists() else ""
            self.assertRegex(calls, r"1883:1883 .*/mosquitto/config/passwd")
