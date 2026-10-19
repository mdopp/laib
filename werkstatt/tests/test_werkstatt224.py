"""#224: the Claude Code token never lands as a literal in /etc/profile.d,
which every user in the container can read."""
import os, stat, subprocess, tempfile, unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent          # werkstatt/


class ClaudeToken(unittest.TestCase):
    def test_the_oauth_token_is_a_0600_file_a_login_shell_reads(self):
        # entrypoint.sh runs with its absolute paths moved into a temp root and
        # su, chown, chpasswd, ssh-keygen and sshd as fakes
        tok = "sk-ant-oat-224secret"
        with tempfile.TemporaryDirectory() as root:
            r, bin_ = Path(root), Path(root) / "bin"
            bin_.mkdir()
            for name in ("su", "chown", "chpasswd", "ssh-keygen", "claude", "git"):
                (bin_ / name).write_text("#!/bin/sh\nexit 0\n")
                (bin_ / name).chmod(0o755)
            script = (HERE / "entrypoint.sh").read_text().replace("/usr/sbin/sshd", "true")
            for p in ("/home/dev", "/run/laib", "/etc/profile.d"):
                script = script.replace(p, root + p)
            (r / "etc" / "profile.d").mkdir(parents=True)
            (r / "entrypoint.sh").write_text(script)
            env = dict(os.environ, PATH=f"{bin_}:{os.environ['PATH']}", CLAUDE_CODE_OAUTH_TOKEN=tok, WERKSTATT_SESSIONS_EVERY="0")
            for k in ("LAIB_TOKEN", "WERKSTATT_GIT_TOKEN", "WERKSTATT_ROLE", "WERKSTATT_SSH_PASSWORD", "WERKSTATT_AUTHORIZED_KEY"):
                env.pop(k, None)
            p = subprocess.run(["bash", str(r / "entrypoint.sh")], env=env, capture_output=True, text=True, timeout=30)
            self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
            for f in (r / "etc" / "profile.d").iterdir():
                self.assertNotIn(tok, f.read_text(), f"{f.name} is world-readable and carries the token")
            files = [f for f in (r / "run" / "laib").iterdir() if tok in f.read_text()]
            self.assertEqual(len(files), 1, "the token lives in one file under /run/laib")
            self.assertEqual(stat.S_IMODE(files[0].stat().st_mode), 0o600)
            got = subprocess.run(["bash", "-c", f'for f in {root}/etc/profile.d/*.sh; do . "$f"; done; printf %s "$CLAUDE_CODE_OAUTH_TOKEN"'],
                                 env={"PATH": os.environ["PATH"]}, capture_output=True, text=True)
            self.assertEqual(got.stdout, tok, "a login shell must still have the token: " + got.stderr)


if __name__ == "__main__":
    unittest.main()
