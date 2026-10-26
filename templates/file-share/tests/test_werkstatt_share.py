"""The Werkstatt share (#666): the Werkstatt's workspace in the LAN share for
the LLDAP admins only, read-write, and what Samba writes there stays the
Werkstatt's dev user's. What the units say; the VM proves the share itself
(vm-install, the file-share/logins step)."""
import re, unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent
WORKSPACE = "${DATA_DIR}/werkstatt/workspace"


def stanza(unit, share):
    m = re.search(r'^Environment="SAMBA_VOLUME_CONFIG_' + share + r'=\[' + share + r'\]; (.*)"$', unit, re.M)
    assert m, f"no Samba share {share}"
    return dict(kv.split("=", 1) for kv in (p.strip() for p in m.group(1).split(";")))


class WerkstattShare(unittest.TestCase):
    def setUp(self):
        self.samba = (HERE / "file-share-samba.container").read_text()

    def test_the_admins_only_read_write(self):
        s = stanza(self.samba, "werkstatt")
        self.assertEqual(s["path"], "/werkstatt")
        self.assertEqual(s["valid users"], "@admins")          # samba-accounts.sh keeps the group
        self.assertEqual(s["access based share enum"], "yes")  # the family does not even see it listed
        self.assertEqual(s["read only"], "no")

    def test_new_files_belong_to_the_werkstatt(self):
        # smbd writes as root (core), then hands a new file to the owner of its folder:
        # the Werkstatt's dev, so a checkout stays dev's and the Werkstatt can still write
        s = stanza(self.samba, "werkstatt")
        self.assertEqual(s["force user"], "root")
        self.assertEqual(s["inherit owner"], "unix only")

    def test_mounted_beside_the_family_share_not_inside(self):
        self.assertIn(f"Volume={WORKSPACE}:/werkstatt\n", self.samba)
        self.assertEqual(stanza(self.samba, "data")["path"], "/data")
        for unit in ("file-share-filebrowser.container", "file-share-syncthing.container"):
            self.assertNotIn("werkstatt", (HERE / unit).read_text(), unit)   # #666: File Browser writes as core, Syncthing syncs to every device

    def test_declared_as_someone_elses_folder(self):
        # a declared data path, so podman finds it and the lint takes it; backed up by the Werkstatt
        laib = (HERE / "laib.yml").read_text()
        self.assertRegex(laib, r"- path: " + re.escape(WORKSPACE) + r"\b.*\n\s+backup: false")


if __name__ == "__main__":
    unittest.main()
