"""#229: scripts/move-servicebay-data.py against a made-up copy of the
Kraftwerk's layout: Vaultwarden flat in its folder, Matter in matter-server/,
Z-Wave JS's server moved to 3001 by an external-settings file and Home
Assistant pointed at it, Syncthing's config in a podman named volume, File
Browser's database in filebrowser-db/, checkouts in claude-dev's and pi-web's
workspaces. The script copies into Laib's layout once; ServiceBay's paths stay
as they were (rollback re-enables ServiceBay's unit).

The script is run the way the wrapper runs it, as a process."""
import json, os, socket, subprocess, sys, tempfile, unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
SCRIPT = HERE / "move-servicebay-data.py"
WRAPPER = HERE / "move-servicebay-data.sh"


def write(p, text=""):
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(text)


class Move(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        t = Path(self.tmp.name)
        self.d, self.q, self.v = t / "stacks", t / "quadlet", t / "volumes"
        self.q.mkdir()
        d = self.d
        write(d / "vaultwarden/db.sqlite3", "vault")
        write(d / "vaultwarden/rsa_key.pem", "made-up")
        write(d / "vaultwarden/attachments/a1", "x")
        write(d / "home-assistant/matter-server/chip.json", "fabric")
        write(d / "home-assistant/zwave-js/settings.json", json.dumps({"zwave": {"port": "/dev/ttyACM0", "securityKeys": {"S0": "made-up"}}, "mqtt": {}}))
        write(d / "home-assistant/zwave-js/sb-external-settings.json", json.dumps({"serverEnabled": True, "serverPort": 3001, "serverHost": "127.0.0.1"}))
        write(d / "home-assistant/homeassistant/.storage/core.config_entries", json.dumps({"version": 1, "data": {"entries": [
            {"domain": "zwave_js", "data": {"url": "ws://localhost:3001", "use_addon": False}},
            {"domain": "matter", "data": {"url": "ws://localhost:5580/ws"}},
            {"domain": "other", "data": {"url": "http://localhost:3001"}}]}}))
        write(self.v / "file-share-syncthing-config/config.xml", "<device id='made-up'/>")
        write(self.v / "file-share-syncthing-config/key.pem", "made-up")
        write(d / "file-share/filebrowser-db/filebrowser.db", "fb")
        write(d / "file-share/filebrowser-config/settings.json", "{}")
        write(d / "claude-dev/workspace/laib/.git/HEAD", "ref")
        write(d / "claude-dev/workspace/notes/.git/HEAD", "ref")
        write(d / "claude-dev/workspace/.claude/history.jsonl", "session")
        write(d / "claude-dev/workspace/home/anna/.bashrc", "")
        write(d / "pi-web/workspace/laib/.git/HEAD", "ref-pi")
        write(d / "pi-web/workspace/scratch.txt", "")
        write(d / "pi-web/data/sessions.db", "")

    def tearDown(self):
        self.tmp.cleanup()

    def run_script(self, *args):
        return subprocess.run([sys.executable, "-I", str(SCRIPT), "--data-dir", str(self.d), "--quadlet", str(self.q),
                               "--volumes", str(self.v), *args], capture_output=True, text=True)

    def test_vaultwarden_moves_under_data(self):
        p = self.run_script("vaultwarden")
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertEqual((self.d / "vaultwarden/data/db.sqlite3").read_text(), "vault")
        self.assertTrue((self.d / "vaultwarden/data/attachments/a1").exists())
        self.assertTrue((self.d / "vaultwarden/db.sqlite3").exists(), "ServiceBay's copy stays for a rollback")

    def test_home_assistant_matter_and_zwave_on_laibs_port(self):
        p = self.run_script("home-assistant")
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertEqual((self.d / "home-assistant/matter/chip.json").read_text(), "fabric")
        z = json.loads((self.d / "home-assistant/zwave-js/settings.json").read_text())["zwave"]
        self.assertEqual((z["serverEnabled"], z["serverPort"], z["serverHost"], z["port"]), (True, 3000, "127.0.0.1", "/dev/ttyACM0"))
        entries = json.loads((self.d / "home-assistant/homeassistant/.storage/core.config_entries").read_text())["data"]["entries"]
        self.assertEqual(entries[0]["data"]["url"], "ws://localhost:3000")
        self.assertEqual(entries[1]["data"]["url"], "ws://localhost:5580/ws")
        self.assertEqual(entries[2]["data"]["url"], "http://localhost:3001", "only Z-Wave JS's entry follows")
        # the edited files keep ServiceBay's version beside them for a rollback
        self.assertIn("3001", (self.d / "home-assistant/homeassistant/.storage/core.config_entries.servicebay").read_text())
        self.assertNotIn("server", (self.d / "home-assistant/zwave-js/settings.json.servicebay").read_text())

    def test_file_share_syncthing_volume_and_filebrowser_db(self):
        p = self.run_script("file-share")
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertEqual((self.d / "file-share/syncthing-config/key.pem").read_text(), "made-up")
        self.assertEqual((self.d / "file-share/filebrowser/filebrowser.db").read_text(), "fb")

    def test_file_share_keeps_sambas_per_user_accounts_in_place(self):
        # ServiceBay's passdb sits where Laib's samba mounts it (#296): nothing is copied, the
        # NT hashes stay, and the operator reads whose accounts carry over and on what condition
        passdb = "samba:1000:XX:AA:[U          ]:LCT-6A000000:\nanna:1001:XX:BB:[U          ]:LCT-6A000001:\nben:1002:XX:CC:[U          ]:LCT-6A000002:\n"
        write(self.d / "file-share/samba-private/smbpasswd", passdb)
        p = self.run_script("file-share")
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertEqual((self.d / "file-share/samba-private/smbpasswd").read_text(), passdb)
        self.assertIn("file-share: Samba's accounts stay in place with their passwords: anna, ben, samba", p.stdout)
        self.assertIn("family or admins", p.stdout)
        self.assertNotIn("BB", p.stdout)

    def test_werkstatt_takes_the_checkouts_only(self):
        p = self.run_script("werkstatt")
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        ws = self.d / "werkstatt/workspace"
        self.assertEqual(sorted(x.name for x in ws.iterdir()), ["laib", "notes", "pi-web-laib"])
        self.assertEqual((ws / "pi-web-laib/.git/HEAD").read_text(), "ref-pi")
        self.assertIn(".claude", p.stdout)      # what stays behind is named

    def test_refuses_while_servicebays_unit_is_there(self):
        write(self.q / "vaultwarden.kube", "[Kube]\nYaml=vaultwarden.yml\n")
        p = self.run_script("vaultwarden")
        self.assertNotEqual(p.returncode, 0)
        self.assertIn("vaultwarden.kube", p.stderr)
        self.assertFalse((self.d / "vaultwarden/data").exists())
        write(self.q / "pi-web.kube", "")
        self.assertNotEqual(self.run_script("werkstatt").returncode, 0)

    def test_refuses_a_target_with_data_and_changes_nothing(self):
        write(self.d / "home-assistant/matter/other.json", "laib's own")
        p = self.run_script("home-assistant")
        self.assertNotEqual(p.returncode, 0)
        self.assertIn("matter", p.stderr)
        self.assertNotIn("serverPort", (self.d / "home-assistant/zwave-js/settings.json").read_text(), "nothing of the service moved")

    def test_dry_run_changes_nothing(self):
        p = self.run_script("--dry-run", "vaultwarden", "home-assistant", "file-share", "werkstatt")
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertFalse((self.d / "vaultwarden/data").exists())
        self.assertFalse((self.d / "werkstatt").exists())
        self.assertIn("vaultwarden/data", p.stdout)

    # --- the review of #237: power cuts, re-runs, running units, space ---

    def test_a_copy_cut_off_midway_is_never_the_target(self):
        # a power cut leaves <target>.partial behind: it is neither data that
        # blocks the next run nor copied along as if it were ServiceBay's
        write(self.d / "vaultwarden/data.partial/db.sqlite3", "half")
        write(self.d / "file-share/syncthing-config.partial/config.xml", "half")
        for svc in ("vaultwarden", "file-share"):
            p = self.run_script(svc)
            self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertEqual(sorted(x.name for x in (self.d / "vaultwarden/data").iterdir()), ["attachments", "db.sqlite3", "rsa_key.pem"])
        self.assertEqual((self.d / "vaultwarden/data/db.sqlite3").read_text(), "vault")
        self.assertFalse((self.d / "vaultwarden/data.partial").exists())
        self.assertEqual((self.d / "file-share/syncthing-config/config.xml").read_text(), "<device id='made-up'/>")
        self.assertFalse((self.d / "file-share/syncthing-config.partial").exists())

    @unittest.skipIf(os.geteuid() == 0, "root reads a 000 file")
    def test_a_failed_copy_leaves_no_target(self):
        bad = self.d / "vaultwarden/attachments/a1"
        bad.chmod(0)
        try:
            p = self.run_script("vaultwarden")
        finally:
            bad.chmod(0o644)
        self.assertNotEqual(p.returncode, 0)
        self.assertFalse((self.d / "vaultwarden/data").exists(), "a half copy sits where the built-in would start on it")
        p = self.run_script("vaultwarden")
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertEqual((self.d / "vaultwarden/data/attachments/a1").read_text(), "x")

    def test_vaultwarden_refuses_a_data_folder_with_data(self):
        write(self.d / "vaultwarden/data/db.sqlite3", "laib's own")
        p = self.run_script("vaultwarden")
        self.assertNotEqual(p.returncode, 0)
        self.assertEqual((self.d / "vaultwarden/data/db.sqlite3").read_text(), "laib's own")
        self.assertFalse((self.d / "vaultwarden/data/rsa_key.pem").exists())

    def test_file_share_refuses_an_existing_database_file(self):
        write(self.d / "file-share/filebrowser/filebrowser.db", "laib's own")
        p = self.run_script("file-share")
        self.assertNotEqual(p.returncode, 0)
        self.assertEqual((self.d / "file-share/filebrowser/filebrowser.db").read_text(), "laib's own")
        self.assertFalse((self.d / "file-share/syncthing-config").exists(), "nothing of the service moved")

    def test_werkstatt_run_twice_copies_nothing_twice(self):
        import shutil
        shutil.rmtree(self.d / "pi-web/workspace/laib")     # no clash with claude-dev's names
        self.assertEqual(self.run_script("werkstatt").returncode, 0)
        p = self.run_script("werkstatt")
        self.assertNotEqual(p.returncode, 0, "a second run is a target that holds data")
        self.assertEqual(sorted(x.name for x in (self.d / "werkstatt/workspace").iterdir()), ["laib", "notes"])

    def test_home_assistant_rerun_keeps_servicebays_version(self):
        self.assertEqual(self.run_script("home-assistant").returncode, 0)
        os.rename(self.d / "home-assistant/matter", self.d / "home-assistant/matter.moved-aside")
        p = self.run_script("home-assistant")
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertIn("3001", (self.d / "home-assistant/homeassistant/.storage/core.config_entries.servicebay").read_text())
        self.assertNotIn("serverPort", (self.d / "home-assistant/zwave-js/settings.json.servicebay").read_text())

    def test_refuses_while_servicebays_unit_still_runs(self):
        # the file moved out, the unit not stopped: a live SQLite copied is no copy,
        # and a running Home Assistant writes its config entries back over the edit
        for svc, active in (("vaultwarden", "vaultwarden.service"), ("home-assistant", "home-assistant.service"), ("werkstatt", "pi-web.service")):
            p = self.run_script("--active", "laib.service " + active, svc)
            self.assertNotEqual(p.returncode, 0, svc)
            self.assertIn(active, p.stderr)
        self.assertFalse((self.d / "vaultwarden/data").exists())
        self.assertFalse((self.d / "home-assistant/matter").exists())
        self.assertNotIn("serverPort", (self.d / "home-assistant/zwave-js/settings.json").read_text())
        self.assertFalse((self.d / "werkstatt").exists())
        self.assertEqual(self.run_script("--active", "laib.service", "vaultwarden").returncode, 0)

    def test_laibs_own_folder_is_not_servicebays_file(self):
        (self.q / "vaultwarden").mkdir()
        p = self.run_script("vaultwarden")
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)

    def test_refuses_without_the_space_for_the_copy(self):
        code = ("import runpy, shutil, sys; shutil.disk_usage = lambda p: shutil._ntuple_diskusage(10**6, 10**6 - 3, 3); "
                "sys.argv = sys.argv[1:]; runpy.run_path(sys.argv[0], run_name='__main__')")
        p = subprocess.run([sys.executable, "-I", "-c", code, str(SCRIPT), "--data-dir", str(self.d), "--quadlet", str(self.q),
                            "--volumes", str(self.v), "vaultwarden"], capture_output=True, text=True)
        self.assertNotEqual(p.returncode, 0)
        self.assertIn("free", p.stderr)
        self.assertFalse((self.d / "vaultwarden/data").exists())

    def test_a_socket_in_a_checkout_does_not_stop_the_copy(self):
        s = socket.socket(socket.AF_UNIX)
        try:
            s.bind(str(self.d / "claude-dev/workspace/laib/.git/fsm.ipc"))
        except OSError as e:
            self.skipTest(f"no unix socket here: {e}")
        try:
            p = self.run_script("werkstatt")
        finally:
            s.close()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertEqual((self.d / "werkstatt/workspace/laib/.git/HEAD").read_text(), "ref")
        self.assertFalse((self.d / "werkstatt/workspace/laib/.git/fsm.ipc").exists())

    def test_wrapper_hands_over_the_running_units_or_stops(self):
        with tempfile.TemporaryDirectory() as b:
            log = Path(b, "argv")
            write(Path(b, "podman"), f'#!/bin/sh\n[ "$1" = volume ] && exit 1\nprintf "%s\\n" "$@" > {log}\n')
            write(Path(b, "systemctl"), "#!/bin/sh\nprintf 'laib.service loaded active running x\\nvaultwarden.service loaded active running y\\n'\n")
            for f in ("podman", "systemctl"):
                Path(b, f).chmod(0o755)
            env = dict(os.environ, PATH=f"{b}:{os.environ['PATH']}", DATA_DIR=str(self.d), QUADLET=str(self.q))
            p = subprocess.run(["bash", str(WRAPPER), "vaultwarden"], env=env, capture_output=True, text=True)
            self.assertEqual(p.returncode, 0, p.stderr)
            argv = log.read_text().split("\n")
            self.assertIn("--active", argv)
            self.assertIn("vaultwarden.service", argv[argv.index("--active") + 1].split())
            log.unlink()
            write(Path(b, "systemctl"), "#!/bin/sh\nexit 1\n")
            p = subprocess.run(["bash", str(WRAPPER), "vaultwarden"], env=env, capture_output=True, text=True)
            self.assertNotEqual(p.returncode, 0, "without the unit list nothing says the unit is stopped")
            self.assertFalse(log.exists())


if __name__ == "__main__":
    unittest.main()
