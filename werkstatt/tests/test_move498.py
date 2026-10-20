"""#498: after the move into the Werkstatt every Claude session hung in the
onboarding dialog, Remote Control said "disconnected — /login" because the
CLAUDE_CODE_OAUTH_TOKEN in the environment outranks the claude.ai login and
cannot open Remote Control, and git could not push for want of gh's login.
Here: the token yields to a login in the kept home, gh's login is wired into
git and both logins are dev's alone, and a session pass answers the
onboarding and the folder trust for each checkout in ~/.claude.json. The
mover's half (claude-dev's home into werkstatt/home) is
scripts/test_move_servicebay_data.py."""
import json, os, shutil, stat, subprocess, sys, tempfile, unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from test_home241 import Box  # noqa: E402

HERE = Path(__file__).resolve().parent.parent          # werkstatt/
TOKEN = "sk-ant-oat01-canary498"


def login_shell_token(box):
    """CLAUDE_CODE_OAUTH_TOKEN as dev's login shell gets it from profile.d"""
    prof = box.root / "etc" / "profile.d"
    p = subprocess.run(["bash", "-c", f'for f in {prof}/*.sh; do . "$f"; done; printf %s "${{CLAUDE_CODE_OAUTH_TOKEN:-}}"'],
                       env={"PATH": os.environ["PATH"], "HOME": str(box.home)}, capture_output=True, text=True)
    return p.stdout


class ClaudeLogin(unittest.TestCase):
    def test_without_a_login_the_token_is_the_sessions_credential(self):
        box = Box(self)
        box.start(CLAUDE_CODE_OAUTH_TOKEN=TOKEN)
        self.assertEqual(login_shell_token(box), TOKEN)

    def test_a_claude_ai_login_in_the_home_wins_over_the_token(self):
        # the token outranks /login and can make model requests only, no Remote Control
        box = Box(self)
        creds = box.home / ".claude" / ".credentials.json"
        creds.parent.mkdir(parents=True)
        creds.write_text('{"claudeAiOauth": "made-up"}')
        creds.chmod(0o644)                      # as a root shell or the move may leave it
        box.start(CLAUDE_CODE_OAUTH_TOKEN=TOKEN)
        self.assertEqual(login_shell_token(box), "", "the token displaces the claude.ai login")
        self.assertEqual(stat.S_IMODE(creds.stat().st_mode), 0o600)
        self.assertEqual(stat.S_IMODE(creds.parent.stat().st_mode), 0o700)


class GhLogin(unittest.TestCase):
    def hosts(self, box):
        h = box.home / ".config" / "gh" / "hosts.yml"
        h.parent.mkdir(parents=True)
        h.write_text("github.com:\n    oauth_token: made-up\n")
        h.chmod(0o666)
        return h

    def test_gh_login_in_the_home_is_how_git_pushes(self):
        box = Box(self)
        h = self.hosts(box)
        argv = box.start()
        self.assertIn("gh auth setup-git", argv)
        self.assertEqual(stat.S_IMODE(h.stat().st_mode), 0o600)

    def test_a_pat_for_the_container_stays_the_credential(self):
        box = Box(self)
        self.hosts(box)
        argv = box.start(WERKSTATT_GIT_TOKEN="github_pat_canary498")
        self.assertNotIn("gh auth setup-git", argv)

    def test_no_gh_login_no_setup(self):
        self.assertNotIn("gh auth setup-git", Box(self).start())


@unittest.skipUnless(shutil.which("tmux") and shutil.which("jq"), "tmux or jq is not installed")
class FirstRun(unittest.TestCase):
    """one werkstatt-sessions pass with a fake claude and real tmux"""

    def setUp(self):
        self.root = Path(tempfile.mkdtemp())
        self.ws, self.home, sock = self.root / "workspace", self.root / "home", self.root / "tmux"
        for d in (self.ws, self.home, sock):
            d.mkdir()
        fake = self.root / "claude"
        fake.write_text("#!/bin/sh\nexec sleep 600\n")
        fake.chmod(0o755)
        self.env = dict(os.environ, HOME=str(self.home), TMUX_TMPDIR=str(sock), WERKSTATT_WORKSPACE=str(self.ws),
                        WERKSTATT_CLAUDE=str(fake), WERKSTATT_TMUX_CONF=str(HERE / "tmux.conf"),
                        GIT_CONFIG_GLOBAL=str(self.root / "gitconfig"))
        self.env.pop("TMUX", None)
        self.cfg = self.home / ".claude.json"
        self.addCleanup(shutil.rmtree, self.root, True)
        self.addCleanup(lambda: subprocess.run(["tmux", "kill-server"], env=self.env, capture_output=True))

    def checkout(self, name, claude_md=True):
        (self.ws / name / ".git").mkdir(parents=True)
        if claude_md:
            (self.ws / name / "CLAUDE.md").write_text("# rules\n")

    def pass_(self):
        p = subprocess.run(["bash", str(HERE / "sessions.sh")], env=self.env, capture_output=True, text=True, timeout=30)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        return p

    def test_onboarding_and_trust_are_answered_for_each_checkout(self):
        self.checkout("laib")
        self.checkout("notes", claude_md=False)        # no session there, nothing to answer
        self.cfg.write_text(json.dumps({"mcpServers": {"laib": {"type": "http"}}, "oauthAccount": {"x": 1},
                                        "projects": {f"{self.ws}/laib": {"allowedTools": ["Bash"]}}}))
        self.cfg.chmod(0o600)
        self.pass_()
        cfg = json.loads(self.cfg.read_text())
        self.assertIs(cfg["hasCompletedOnboarding"], True)
        self.assertIs(cfg["projects"][f"{self.ws}/laib"]["hasTrustDialogAccepted"], True)
        self.assertEqual(cfg["projects"][f"{self.ws}/laib"]["allowedTools"], ["Bash"], "the project's own keys stay")
        self.assertEqual((cfg["mcpServers"], cfg["oauthAccount"]), ({"laib": {"type": "http"}}, {"x": 1}))
        self.assertNotIn(f"{self.ws}/notes", cfg["projects"])
        self.assertEqual(stat.S_IMODE(self.cfg.stat().st_mode), 0o600)

    def test_a_fresh_home_gets_a_config(self):
        self.checkout("laib")
        self.pass_()
        cfg = json.loads(self.cfg.read_text())
        self.assertIs(cfg["projects"][f"{self.ws}/laib"]["hasTrustDialogAccepted"], True)

    def test_an_answered_config_is_not_rewritten(self):
        # running sessions write ~/.claude.json too: a pass with nothing to add leaves it alone
        self.checkout("laib")
        self.pass_()
        ino = self.cfg.stat().st_ino
        self.pass_()
        self.assertEqual(self.cfg.stat().st_ino, ino)

    def test_a_config_that_is_not_json_is_left_alone(self):
        self.checkout("laib")
        self.cfg.write_text("{half")
        p = self.pass_()
        self.assertEqual(self.cfg.read_text(), "{half")
        self.assertIn("not JSON", p.stderr)
        self.assertEqual([f.name for f in self.home.iterdir()], [".claude.json"], "a temp file was left behind")


if __name__ == "__main__":
    unittest.main()
