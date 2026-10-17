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


def load_mover():
    import importlib.util
    spec = importlib.util.spec_from_file_location("mover", SCRIPT)
    m = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(m)
    return m


ST_CONFIG = """<configuration version="37">
    <folder id="alice-cam" label="Alice Kamera" path="/var/syncthing/Sync/alice/photos/Camera" type="sendreceive">
        <device id="PHONE-ALICE" introducedBy=""></device>
        <versioning></versioning>
    </folder>
    <folder id="shared-fotos" label="Fotos" path="~/Sync/photos" type="sendreceive">
        <device id="LAPTOP" introducedBy=""></device>
        <versioning></versioning>
    </folder>
    <folder id="docs" label="Dokumente" path="/var/syncthing/Sync/documents" type="sendreceive">
        <versioning></versioning>
    </folder>
    <device id="BOX-ID" name="box"></device>
    <defaults>
        <folder id="" label="" path="~" type="sendreceive"></folder>
    </defaults>
</configuration>
"""


class ImmichLibraries(unittest.TestCase):
    """#263: ServiceBay mounted all of file-share/data at /mnt/photos; its
    disk import made one external library per box user on
    /mnt/photos/<user>/photos and a Shared one on /mnt/photos/photos. Laib
    mounts file-share/data/photos there. Each library becomes one folder
    under photos/ (Shared as photos/shared), so no library holds another's
    photos; Immich's paths are rewritten (in the real-Postgres job,
    scripts/immich-move-test.py) and the Syncthing folders follow."""

    LIBS = [
        {"id": "L-shared", "name": "Shared", "importPaths": ["/mnt/photos/photos"], "deleted": False},
        {"id": "L-alice", "name": "alice", "importPaths": ["/mnt/photos/alice/photos"], "deleted": False},
        {"id": "L-bob", "name": "bob", "importPaths": ["/mnt/photos/bob/photos/"], "deleted": False},
        {"id": "L-up", "name": "elsewhere", "importPaths": ["/srv/old"], "deleted": False},
    ]

    def setUp(self):
        self.m = load_mover()
        self.tmp = tempfile.TemporaryDirectory()
        t = Path(self.tmp.name)
        self.d, self.q, self.v = t / "stacks", t / "quadlet", t / "volumes"
        self.q.mkdir()
        self.base = self.d / "file-share/data"
        write(self.base / "photos/2019 Urlaub/a.jpg", "shared-a")
        write(self.base / "photos/.stfolder/x", "")
        write(self.base / "alice/photos/Camera/b.jpg", "alice-b")
        write(self.base / "alice/documents/c.pdf", "stays")
        write(self.base / "bob/photos/d.jpg", "bob-d")

    def tearDown(self):
        self.tmp.cleanup()

    def run_script(self, *args):
        return subprocess.run([sys.executable, "-I", str(SCRIPT), "--data-dir", str(self.d), "--quadlet", str(self.q),
                               "--volumes", str(self.v), *args], capture_output=True, text=True)

    def test_each_library_becomes_one_folder_under_photos(self):
        rewrites, moves, notes = self.m.photo_plan(self.LIBS)
        self.assertEqual(rewrites, [("L-shared", "/mnt/photos/photos", "/mnt/photos/shared"),
                                    ("L-alice", "/mnt/photos/alice/photos", "/mnt/photos/alice"),
                                    ("L-bob", "/mnt/photos/bob/photos/", "/mnt/photos/bob")])
        self.assertEqual(moves, [("photos", "photos/shared"), ("alice/photos", "photos/alice"), ("bob/photos", "photos/bob")])
        self.assertTrue(any("/srv/old" in n for n in notes), "a library outside the share is named, not touched")

    def test_refuses_what_servicebay_never_made(self):
        for paths in (["/mnt/photos"], ["/mnt/photos/alice"], ["/mnt/photos/alice/photos/2020"], ["/mnt/photos/shared/photos"]):
            with self.assertRaises(self.m.Refused, msg=paths):
                self.m.photo_plan([{"id": "L", "name": "x", "importPaths": paths, "deleted": False}])

    def test_photos_move_and_a_rerun_or_a_cut_off_run_ends_the_same(self):
        _, moves, _ = self.m.photo_plan(self.LIBS)
        journal = str(self.d / "immich/library-move.json")
        os.makedirs(self.d / "immich")
        real, calls = os.rename, []

        def cut(a, b):
            calls.append(a)
            if len(calls) == 3:
                raise OSError("power cut")
            real(a, b)
        from unittest import mock
        with mock.patch.object(self.m.os, "rename", side_effect=cut):
            with self.assertRaises(OSError):
                self.m.move_photos(str(self.base), moves, journal)
        self.m.move_photos(str(self.base), moves, journal)
        self.m.move_photos(str(self.base), moves, journal)
        p = self.base / "photos"
        self.assertEqual(sorted(x.name for x in p.iterdir()), ["alice", "bob", "shared"])
        self.assertEqual((p / "shared/2019 Urlaub/a.jpg").read_text(), "shared-a")
        self.assertTrue((p / "shared/.stfolder").is_dir(), "Syncthing's folder marker moves with its folder")
        self.assertEqual((p / "alice/Camera/b.jpg").read_text(), "alice-b")
        self.assertEqual((p / "bob/d.jpg").read_text(), "bob-d")
        self.assertFalse((self.base / "alice/photos").exists())
        self.assertEqual((self.base / "alice/documents/c.pdf").read_text(), "stays")
        self.assertEqual(sorted(x.name for x in self.base.iterdir()), ["alice", "bob", "photos"], "no temporary name left behind")

    def test_syncthing_folders_follow_their_photos_with_their_ids(self):
        _, moves, _ = self.m.photo_plan(self.LIBS)
        text, changes = self.m.syncthing_repoint(ST_CONFIG, moves)
        import xml.etree.ElementTree as ET
        root = ET.fromstring(text)
        paths = {f.get("id"): f.get("path") for f in root.findall("folder")}
        self.assertEqual(paths, {"alice-cam": "/var/syncthing/Sync/photos/alice/Camera",
                                 "shared-fotos": "/var/syncthing/Sync/photos/shared",
                                 "docs": "/var/syncthing/Sync/documents"})
        self.assertEqual([d.get("id") for d in root.iter("device") if d.get("name") == "box"], ["BOX-ID"])
        self.assertEqual(root.find("defaults/folder").get("path"), "~", "the defaults are no folder")
        self.assertEqual(sorted(c[0] for c in changes), ["alice-cam", "shared-fotos"])
        self.assertEqual(self.m.syncthing_repoint(text, [])[1], [])

    def test_syncthing_refuses_a_folder_the_move_would_empty_or_fill(self):
        _, moves, _ = self.m.photo_plan(self.LIBS)
        whole = ST_CONFIG.replace("/var/syncthing/Sync/documents", "/var/syncthing/Sync/alice")
        with self.assertRaisesRegex(self.m.Refused, "docs"):
            self.m.syncthing_repoint(whole, moves)       # its peers would delete alice's photos
        _, only_users, _ = self.m.photo_plan(self.LIBS[1:2])
        with self.assertRaisesRegex(self.m.Refused, "shared-fotos"):
            self.m.syncthing_repoint(ST_CONFIG, only_users)  # alice's photos would go to the family's laptop

    def test_immich_refuses_while_syncthing_or_immich_runs(self):
        write(self.d / "immich/pgdata/PG_VERSION", "14")
        for unit in ("file-share-syncthing.service", "file-share.service", "immich-db.service", "immich.service"):
            p = self.run_script("--active", "laib.service " + unit, "--pg-socket", str(self.d), "immich")
            self.assertNotEqual(p.returncode, 0, unit)
            self.assertIn(unit, p.stderr)
        self.assertTrue((self.base / "alice/photos/Camera/b.jpg").exists())

    def test_immich_waits_for_file_shares_syncthing_config(self):
        write(self.d / "immich/pgdata/PG_VERSION", "14")
        write(self.v / "file-share-syncthing-config/config.xml", ST_CONFIG)
        p = self.run_script("--pg-socket", str(self.d), "immich")
        self.assertNotEqual(p.returncode, 0)
        self.assertIn("file-share", p.stderr)
        self.assertTrue((self.base / "alice/photos/Camera/b.jpg").exists())

    def test_wrapper_never_starts_a_second_postgres_on_immichs_data(self):
        write(self.d / "immich/pgdata/PG_VERSION", "14")
        with tempfile.TemporaryDirectory() as b:
            log = Path(b, "calls")
            # a container of ServiceBay's immich pod still has the database open
            write(Path(b, "podman"), f"""#!/bin/sh
echo "$*" >> {log}
case "$1" in
  volume) exit 1;;
  ps) echo c1;;
  inspect) echo "immich-database {self.d}/immich/pgdata";;
esac
""")
            write(Path(b, "systemctl"), "#!/bin/sh\necho 'laib.service loaded active running x'\n")
            for f in ("podman", "systemctl"):
                Path(b, f).chmod(0o755)
            env = dict(os.environ, PATH=f"{b}:{os.environ['PATH']}", DATA_DIR=str(self.d), QUADLET=str(self.q))
            p = subprocess.run(["bash", str(WRAPPER), "immich"], env=env, capture_output=True, text=True)
            self.assertNotEqual(p.returncode, 0)
            self.assertIn("immich-database", p.stderr)
            self.assertNotIn("run ", log.read_text(), "no Postgres and no mover started")


if __name__ == "__main__":
    unittest.main()
