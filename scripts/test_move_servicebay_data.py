"""#229: scripts/move-servicebay-data.py against a made-up copy of the
Kraftwerk's layout: Vaultwarden flat in its folder, Matter in matter-server/,
Z-Wave JS's server moved to 3001 by an external-settings file and Home
Assistant pointed at it, Syncthing's config in a podman named volume, File
Browser's database in filebrowser-db/, checkouts in claude-dev's and pi-web's
workspaces. The script copies into Laib's layout once; ServiceBay's paths stay
as they were (rollback re-enables ServiceBay's unit).

The script is run the way the wrapper runs it, as a process."""
import json, os, subprocess, sys, tempfile, unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
SCRIPT = HERE / "move-servicebay-data.py"


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
            {"domain": "matter", "data": {"url": "ws://localhost:5580/ws"}}]}}))
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
        # the edited files keep ServiceBay's version beside them for a rollback
        self.assertIn("3001", (self.d / "home-assistant/homeassistant/.storage/core.config_entries.servicebay").read_text())
        self.assertNotIn("server", (self.d / "home-assistant/zwave-js/settings.json.servicebay").read_text())

    def test_file_share_syncthing_volume_and_filebrowser_db(self):
        p = self.run_script("file-share")
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertEqual((self.d / "file-share/syncthing-config/key.pem").read_text(), "made-up")
        self.assertEqual((self.d / "file-share/filebrowser/filebrowser.db").read_text(), "fb")

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


if __name__ == "__main__":
    unittest.main()
