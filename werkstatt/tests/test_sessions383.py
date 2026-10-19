"""#383: the Werkstatt starts one Claude Code Remote Control session per
checkout, as claude-dev did: a window in the tmux session `claude` per
checkout with a CLAUDE.md, named after it, `--continue` with a fresh start
as fallback, a marker that keeps one off across restarts, new checkouts
picked up by the next pass, a dead session started again. Real tmux, a
fake claude; the real image under systemd is scripts/boot-werkstatt.sh."""
import os, shutil, subprocess, tempfile, time, unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent          # werkstatt/
SCRIPT = HERE / "sessions.sh"


@unittest.skipUnless(shutil.which("tmux"), "tmux is not installed")
class Sessions(unittest.TestCase):
    def setUp(self):
        self.root = Path(tempfile.mkdtemp())
        self.ws, self.home, sock = self.root / "workspace", self.root / "home", self.root / "tmux"
        for d in (self.ws, self.home, sock):
            d.mkdir()
        self.log = self.root / "claude.log"
        fake = self.root / "claude"
        # records where it ran and how; stays up like a session, or fails when told to
        fake.write_text('#!/bin/sh\nprintf "%s %s\\n" "$(basename "$PWD")" "$*" >> "$CLAUDE_LOG"\n'
                        '[ -e "$CLAUDE_FAIL" ] && exit 1\nexec sleep 600\n')
        fake.chmod(0o755)
        self.fail_flag = self.root / "fail"
        self.env = dict(os.environ, HOME=str(self.home), TMUX_TMPDIR=str(sock), WERKSTATT_WORKSPACE=str(self.ws),
                        WERKSTATT_CLAUDE=str(fake), WERKSTATT_TMUX_CONF=str(HERE / "tmux.conf"),
                        CLAUDE_LOG=str(self.log), CLAUDE_FAIL=str(self.fail_flag), GIT_CONFIG_GLOBAL=str(self.root / "gitconfig"))
        self.env.pop("TMUX", None)
        self.addCleanup(shutil.rmtree, self.root, True)
        self.addCleanup(lambda: subprocess.run(["tmux", "kill-server"], env=self.env, capture_output=True))

    def checkout(self, name, claude_md=True):
        d = self.ws / name
        (d / ".git").mkdir(parents=True)
        if claude_md:
            (d / "CLAUDE.md").write_text("# rules\n")

    def run_(self, *args):
        p = subprocess.run(["bash", str(SCRIPT), *args], env=self.env, capture_output=True, text=True, timeout=30)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        return p.stdout

    def windows(self):
        p = subprocess.run(["tmux", "list-windows", "-t", "=claude", "-F", "#{window_name}"], env=self.env, capture_output=True, text=True)
        return sorted(p.stdout.split())

    def wait_log(self, n):
        for _ in range(50):
            if self.log.exists() and len(self.log.read_text().splitlines()) >= n:
                return self.log.read_text().splitlines()
            time.sleep(0.1)
        self.fail(f"claude ran fewer than {n} times: {self.log.read_text() if self.log.exists() else ''}")

    def test_one_window_per_checkout_with_a_claude_md(self):
        self.checkout("laib")
        self.checkout("haus")
        self.checkout("content", claude_md=False)       # a content repo is not a development target
        self.run_()
        self.assertEqual(self.windows(), ["haus", "laib"])
        runs = self.wait_log(2)
        self.assertIn("laib --continue --remote-control laib", runs)
        self.assertIn("haus --continue --remote-control haus", runs)

    def test_no_session_gets_the_permission_bypass(self):
        # reachable from the phone, holding an apply-capable hand (#383)
        code = [l for l in SCRIPT.read_text().splitlines() if not l.lstrip().startswith("#")]
        self.assertFalse([l for l in code if "skip-permissions" in l], "a session may bypass Claude Code's permissions")

    def test_a_pass_is_idempotent_and_picks_up_a_new_checkout(self):
        self.checkout("laib")
        self.run_()
        self.run_()
        self.assertEqual(self.windows(), ["laib"])
        self.checkout("flut")
        self.run_()
        self.assertEqual(self.windows(), ["flut", "laib"])
        self.assertEqual(len(self.wait_log(2)), 2, "a pass started a session that was running")

    def test_off_survives_a_restart_and_on_brings_it_back(self):
        self.checkout("laib")
        self.checkout("servicebay")
        self.run_()
        self.run_("off", "servicebay")
        self.assertEqual(self.windows(), ["laib"])
        self.assertTrue((self.home / ".werkstatt" / "no-autostart" / "servicebay").exists())
        subprocess.run(["tmux", "kill-server"], env=self.env)   # a container restart
        self.run_()
        self.assertEqual(self.windows(), ["laib"], "the switched-off session came back after a restart")
        self.run_("on", "servicebay")
        self.assertEqual(self.windows(), ["laib", "servicebay"])

    def test_a_failed_session_stays_visible_and_is_started_again(self):
        self.checkout("laib")
        self.fail_flag.write_text("")
        self.run_()
        self.wait_log(2)                                   # --continue, then the fresh start, both fail
        self.assertEqual(self.windows(), ["laib"], "a failed session took its window with it")
        self.fail_flag.unlink()
        out = self.run_()
        self.assertIn("started again", out)
        self.assertEqual(self.wait_log(3)[-1], "laib --continue --remote-control laib")

    def test_an_empty_workspace_still_has_the_session(self):
        self.run_()
        self.assertEqual(len(self.windows()), 1)
        # the first checkout replaces the empty window: one window per checkout
        self.checkout("laib")
        self.run_()
        self.assertEqual(self.windows(), ["laib"])

    def test_names_that_would_break_a_target_are_refused(self):
        for bad in ("-x", "a:b", ".hidden", ""):
            p = subprocess.run(["bash", str(SCRIPT), "off", bad], env=self.env, capture_output=True, text=True)
            self.assertEqual(p.returncode, 2, bad)


class Wiring(unittest.TestCase):
    def test_the_image_carries_the_script_and_the_tmux_config(self):
        cf = (HERE / "Containerfile").read_text()
        self.assertRegex(cf, r"COPY sessions\.sh /usr/local/bin/werkstatt-sessions\b")
        self.assertRegex(cf, r"COPY tmux\.conf /etc/tmux\.conf\b")

    def test_the_entrypoint_runs_a_pass_before_sshd_and_then_on_a_timer(self):
        entry = (HERE / "entrypoint.sh").read_text()
        first, sshd = entry.find("werkstatt-sessions"), entry.find("exec /usr/sbin/sshd")
        self.assertGreater(first, 0, "the entrypoint starts no sessions")
        self.assertLess(first, sshd, "the sessions start after sshd took over PID 1")
        self.assertIn("WERKSTATT_SESSIONS_EVERY", entry)


if __name__ == "__main__":
    unittest.main()
