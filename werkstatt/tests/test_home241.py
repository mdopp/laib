"""#241: the Werkstatt's home is a data path. The image's dotfiles go into an
empty home once, the person's files are never overwritten, sshd's host keys
are made once and kept, and the entrypoint writes no secret into the home.
The real restart and image update run in scripts/boot-werkstatt.sh."""
import os, re, shutil, subprocess, tempfile, unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent          # werkstatt/
ROOT = HERE.parent
TPL = ROOT / "templates" / "werkstatt"
# the entrypoint's absolute paths, moved under a temp root
PATHS = ("/home/dev", "/run/laib", "/etc/profile.d", "/etc/ssh/keys", "/opt/werkstatt/home-seed", "/usr/sbin/sshd")
SECRETS = {"CLAUDE_CODE_OAUTH_TOKEN": "sk-ant-oat01-canary241", "LAIB_TOKEN": "lb_canary241",
           "WERKSTATT_GIT_TOKEN": "github_pat_canary241", "WERKSTATT_SSH_PASSWORD": "pw-canary241"}


class Box:
    """entrypoint.sh in a temp root: su, chown, chpasswd, claude and git as
    fakes, sshd records its argv, ssh-keygen is the real one (but -A, which
    fills the image's /etc/ssh, is refused)."""

    def __init__(self, test):
        self.root = Path(tempfile.mkdtemp())
        test.addCleanup(shutil.rmtree, self.root, True)
        self.test, self.log = test, self.root / "argv.log"
        bin_ = self.root / "bin"
        bin_.mkdir()
        keygen = shutil.which("ssh-keygen")
        fakes = {"su": 'shift 2; exec bash -c "$1"', "chown": "", "gh": "", "chpasswd": "cat >/dev/null", "claude": "", "git": "",
                 "python3": "", "ssh-keygen": f'[ "$1" = -A ] && {{ echo "-A fills the image" >&2; exit 1; }}; exec {keygen} "$@"'}
        for name, body in fakes.items():
            (bin_ / name).write_text(f'#!/bin/bash\nprintf "%s\\n" "{name} $*" >> "$ARGV_LOG"\n{body}\n')
            (bin_ / name).chmod(0o755)
        sshd = self.root / "usr" / "sbin" / "sshd"
        sshd.parent.mkdir(parents=True)
        sshd.write_text('#!/bin/bash\nprintf "%s\\n" "sshd $*" >> "$ARGV_LOG"\n')
        sshd.chmod(0o755)
        seed = self.root / "opt" / "werkstatt" / "home-seed"
        seed.mkdir(parents=True)
        (seed / ".bashrc").write_text("# the image's bashrc\n")
        (seed / ".profile").write_text("# the image's profile\n")
        for d in ("etc/profile.d", "etc/ssh/keys", "home/dev"):   # laib makes the data paths before the unit starts, empty
            (self.root / d).mkdir(parents=True, exist_ok=True)
        script = (HERE / "entrypoint.sh").read_text()
        for p in PATHS:
            script = script.replace(p, str(self.root) + p)
        (self.root / "entrypoint.sh").write_text(script)
        self.env = dict(os.environ, PATH=f"{bin_}:{os.environ['PATH']}", ARGV_LOG=str(self.log),
                        LAIB_MCP_URL="http://host.containers.internal:5900/mcp", WERKSTATT_SESSIONS_EVERY="0")
        for k in SECRETS:
            self.env.pop(k, None)
        self.home, self.keys = self.root / "home" / "dev", self.root / "etc" / "ssh" / "keys"

    def start(self, **env):
        self.log.write_text("")
        p = subprocess.run(["bash", str(self.root / "entrypoint.sh")], env=dict(self.env, **env),
                           capture_output=True, text=True, timeout=60)
        self.test.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        return self.log.read_text()

    def key_bytes(self):
        return {p.name: p.read_bytes() for p in sorted(self.keys.iterdir())}


class Home(unittest.TestCase):
    def test_the_first_start_seeds_the_images_dotfiles_into_the_empty_home(self):
        box = Box(self)
        box.start()
        self.assertEqual((box.home / ".bashrc").read_text(), "# the image's bashrc\n")
        self.assertEqual((box.home / ".profile").read_text(), "# the image's profile\n")

    def test_a_later_start_keeps_the_persons_files(self):
        box = Box(self)
        box.start()
        (box.home / ".bashrc").write_text("# mine\n")
        (box.home / ".profile").unlink()                      # deleted on purpose: not brought back
        sess = box.home / ".claude" / "projects" / "-workspace-laib" / "s.jsonl"
        sess.parent.mkdir(parents=True)
        sess.write_text('{"type":"user"}\n')
        (box.home / ".claude.json").write_text('{"projects":{}}\n')
        box.start()
        self.assertEqual((box.home / ".bashrc").read_text(), "# mine\n")
        self.assertFalse((box.home / ".profile").exists(), "a later start seeded the home again")
        self.assertEqual(sess.read_text(), '{"type":"user"}\n')
        self.assertEqual((box.home / ".claude.json").read_text(), '{"projects":{}}\n')

    def test_a_home_that_already_has_files_is_not_seeded(self):
        # claude-dev's home copied in by the migration, or a restore
        box = Box(self)
        (box.home / ".claude.json").write_text("{}\n")
        box.start()
        self.assertFalse((box.home / ".bashrc").exists(), "the image's dotfiles went into a home that was not empty")


class HostKeys(unittest.TestCase):
    def test_host_keys_are_made_once_and_kept(self):
        box = Box(self)
        argv = box.start()
        first = box.key_bytes()
        self.assertIn("ssh_host_ed25519_key", first)
        self.assertIn("ssh_host_ed25519_key.pub", first)
        sshd = [l for l in argv.splitlines() if l.startswith("sshd ")]
        self.assertEqual(len(sshd), 1, argv)
        self.assertIn(f"-h {box.keys}/ssh_host_ed25519_key", sshd[0], "sshd serves the kept key, not the image's")
        box.start()
        self.assertEqual(box.key_bytes(), first, "a start made new host keys")

    def test_the_image_ships_no_host_key(self):
        # every box pulls the same public image: a key baked into it is everybody's key
        self.assertRegex((HERE / "Containerfile").read_text(), r"rm -f /etc/ssh/ssh_host_\*")
        self.assertNotIn("ssh-keygen -A", (HERE / "entrypoint.sh").read_text())


class NoSecretInTheHome(unittest.TestCase):
    def test_the_entrypoint_writes_no_secret_into_the_home_or_the_keys(self):
        box = Box(self)
        box.start(WERKSTATT_AUTHORIZED_KEY="ssh-ed25519 AAAAC3Nza test@box", **SECRETS)
        self.assertTrue((box.home / ".ssh" / "authorized_keys").exists())
        for d in (box.home, box.keys):
            for f in d.rglob("*"):
                if f.is_file():
                    text = f.read_bytes().decode(errors="replace")
                    for name, value in SECRETS.items():
                        self.assertNotIn(value, text, f"{name} is in {f.relative_to(box.root)}")


class Template(unittest.TestCase):
    def volumes(self, unit):
        return dict(l.split("=", 1)[1].rsplit(":", 1)[0].split(":", 1)
                    for l in (TPL / unit).read_text().splitlines() if l.startswith("Volume="))

    def test_the_home_cache_and_keys_are_declared_data_paths(self):
        vols = self.volumes("werkstatt.container")
        self.assertEqual(vols.get("${DATA_DIR}/werkstatt/home"), "/home/dev")
        self.assertEqual(vols.get("${DATA_DIR}/werkstatt/cache"), "/home/dev/.cache")
        self.assertEqual(vols.get("${DATA_DIR}/werkstatt/ssh-host-keys"), "/etc/ssh/keys")
        self.assertEqual(vols.get("${DATA_DIR}/werkstatt/workspace"), "/workspace", "the workspace stays as it is")
        contract = (TPL / "laib.yml").read_text()
        entries = re.findall(r"(?m)^  - path: (\S+)(?:[^\n]*\n    backup: (\w+))?", contract)
        backup = {p: b != "false" for p, b in entries}
        for src in vols:
            self.assertIn(src, backup, f"{src} is mounted but not a declared data path")
        self.assertTrue(backup["${DATA_DIR}/werkstatt/home"], "the home is in the backup")
        self.assertTrue(backup["${DATA_DIR}/werkstatt/ssh-host-keys"], "the host keys are in the backup")
        self.assertFalse(backup["${DATA_DIR}/werkstatt/cache"], "caches are rebuilt, not backed up")

    def test_the_autoloop_does_not_share_the_home(self):
        # pi's container keeps its own hand (#44); one ~/.claude.json, one writer
        self.assertNotIn("/home/dev", self.volumes("werkstatt-autoloop.container").values())

    def test_package_caches_live_under_the_cache(self):
        cf = (HERE / "Containerfile").read_text()
        for var in ("NPM_CONFIG_CACHE", "GOMODCACHE", "GOCACHE", "PIP_CACHE_DIR"):
            self.assertRegex(cf, var + r"=/home/dev/\.cache/", f"{var} would land in the backed-up home")


if __name__ == "__main__":
    unittest.main()
