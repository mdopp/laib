"""#383: the Werkstatt starts one Claude Code Remote Control session per
checkout, as claude-dev did: a window in the tmux session `claude` per
checkout with a CLAUDE.md, named after it, `--continue` with a fresh start
as fallback, a marker that keeps one off across restarts, new checkouts
picked up by the next pass, a dead session started again. Real tmux, a
fake claude; the real image under systemd is scripts/boot-werkstatt.sh."""
import getpass, json, os, shutil, subprocess, tempfile, time, unittest
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
        # records where it ran and how; stays up like a session, fails when told to,
        # or ends with an error later, as a session that ran for hours can (#498)
        fake.write_text('#!/bin/sh\nprintf "%s %s\\n" "$(basename "$PWD")" "$*" >> "$CLAUDE_LOG"\n'
                        '[ -e "$CLAUDE_FAIL" ] && exit 1\n'
                        'i=0; while [ $i -lt 6000 ]; do [ -e "$CLAUDE_STOP" ] && exit 1; sleep 0.1; i=$((i+1)); done\n')
        fake.chmod(0o755)
        self.fail_flag, self.stop_flag = self.root / "fail", self.root / "stop"
        self.env = dict(os.environ, HOME=str(self.home), TMUX_TMPDIR=str(sock), WERKSTATT_WORKSPACE=str(self.ws),
                        WERKSTATT_CLAUDE=str(fake), WERKSTATT_TMUX_CONF=str(HERE / "tmux.conf"),
                        CLAUDE_LOG=str(self.log), CLAUDE_FAIL=str(self.fail_flag), CLAUDE_STOP=str(self.stop_flag),
                        WERKSTATT_USER=getpass.getuser(), GIT_CONFIG_GLOBAL=str(self.root / "gitconfig"))
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
        self.assertIn("laib --permission-mode default --continue --remote-control laib", runs)
        self.assertIn("haus --permission-mode default --continue --remote-control haus", runs)

    def test_the_permission_mode_is_the_owners_value(self):
        # #898: default asks (#383); bypass runs without asking, the owner's choice.
        # Passed on every start, so a --continue does not keep its old mode.
        self.checkout("laib")
        self.run_()
        self.assertIn("laib --permission-mode default --continue --remote-control laib", self.wait_log(1))
        self.assertFalse((self.home / ".claude" / "settings.json").exists(), "default mode answers no bypass warning")
        subprocess.run(["tmux", "kill-server"], env=self.env, capture_output=True)
        self.env["WERKSTATT_PERMISSION_MODE"] = "bypass"
        self.run_()
        self.assertIn("laib --dangerously-skip-permissions --continue --remote-control laib", self.wait_log(2))
        settings = json.loads((self.home / ".claude" / "settings.json").read_text())
        self.assertIs(settings.get("skipDangerousModePermissionPrompt"), True, "nobody is at the window to accept the warning")
        self.env["WERKSTATT_PERMISSION_MODE"] = "yes please"
        subprocess.run(["tmux", "kill-server"], env=self.env, capture_output=True)
        self.run_()
        self.assertIn("laib --permission-mode default --continue --remote-control laib", self.wait_log(3)[2:])

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

    def test_a_switched_off_session_does_not_start_itself_fresh(self):
        # #498: `off flut` at 15:31, and at 18:11 the window's own fallback
        # started `claude --remote-control flut`: its --continue had ended
        self.checkout("flut")
        self.run_()
        self.assertEqual(self.wait_log(1), ["flut --permission-mode default --continue --remote-control flut"])
        off = self.home / ".werkstatt" / "no-autostart"
        off.mkdir(parents=True)
        (off / "flut").write_text("")       # the marker, with the window still up
        self.stop_flag.write_text("")       # its --continue ends with an error
        time.sleep(1.5)
        self.assertEqual(self.log.read_text().splitlines(), ["flut --permission-mode default --continue --remote-control flut"],
                         "a switched-off session started itself fresh")
        self.run_()
        self.assertEqual(self.log.read_text().splitlines(), ["flut --permission-mode default --continue --remote-control flut"], "the pass started it")

    def test_the_fresh_start_still_follows_a_failed_continue(self):
        self.checkout("laib")
        self.stop_flag.write_text("")       # --continue fails: no conversation yet
        self.run_()
        self.assertEqual(self.wait_log(2), ["laib --permission-mode default --continue --remote-control laib", "laib --permission-mode default --remote-control laib"])

    def test_off_as_another_user_refuses_and_writes_no_marker(self):
        # #498: as root, `off` wrote root's marker, killed nothing in root's
        # tmux and said it had; the session went on
        self.checkout("flut")
        self.run_()
        p = subprocess.run(["bash", str(SCRIPT), "off", "flut"], env=dict(self.env, WERKSTATT_USER="dev-" + getpass.getuser()),
                           capture_output=True, text=True, timeout=30)
        self.assertEqual(p.returncode, 2, p.stdout + p.stderr)
        self.assertIn("podman exec -u dev-", p.stderr)
        self.assertFalse((self.home / ".werkstatt" / "no-autostart" / "flut").exists())
        self.assertEqual(self.windows(), ["flut"])

    def test_a_failed_session_stays_visible_and_is_started_again(self):
        self.checkout("laib")
        self.fail_flag.write_text("")
        self.run_()
        self.wait_log(2)                                   # --continue, then the fresh start, both fail
        self.assertEqual(self.windows(), ["laib"], "a failed session took its window with it")
        self.fail_flag.unlink()
        out = self.run_()
        self.assertIn("started again", out)
        self.assertEqual(self.wait_log(3)[-1], "laib --permission-mode default --continue --remote-control laib")

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



class ClaudeTree(unittest.TestCase):
    def test_the_entrypoint_gives_dev_claude_codes_tree_back(self):
        """2026-10-25: ~/.claude/projects was root's, so haus-android and laib-608 never saved a
        conversation and came back empty after the restart (Transcript writes are failing, EACCES)"""
        src = (Path(__file__).resolve().parent.parent / "entrypoint.sh").read_text()
        self.assertIn("find /home/dev/.claude -xdev ! -user dev -exec chown dev:dev {} + ||", src)

if __name__ == "__main__":
    unittest.main()
