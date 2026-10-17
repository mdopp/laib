package stick

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runPreInstall runs usb/pre-install.sh against shimmed tools. The box has
// an 8 GiB virtio OS disk (vda) and a 16 GiB data disk (vdb, an md member);
// installed=true puts an old Fedora CoreOS on vda (EFI, boot, xfs root).
func runPreInstall(t *testing.T, installed, reinstall bool) (log string, out string, err error) {
	t.Helper()
	root := t.TempDir()
	bin, sys, inst := filepath.Join(root, "bin"), filepath.Join(root, "sys"), filepath.Join(root, "installer.d")
	for _, d := range []string{bin, filepath.Join(sys, "block/vda"), filepath.Join(sys, "block/vdb")} {
		os.MkdirAll(d, 0o755)
	}
	os.WriteFile(filepath.Join(sys, "block/vda/removable"), []byte("0\n"), 0o644)
	os.WriteFile(filepath.Join(sys, "block/vdb/removable"), []byte("0\n"), 0o644)
	logf := filepath.Join(root, "shim.log")
	parts := `/dev/vda`
	labels := ``
	partlabels := `/dev/vda `
	if installed {
		parts = "/dev/vda\n/dev/vda1\n/dev/vda2\n/dev/vda3\n/dev/vda4"
		labels = "\nEFI-SYSTEM\nboot\nroot"
		partlabels = "/dev/vda \n/dev/vda1 BIOS-BOOT\n/dev/vda2 EFI-SYSTEM\n/dev/vda3 boot\n/dev/vda4 root"
	}
	shim(t, bin, "lsblk", `case "$*" in
  "-ndo PKNAME"*) ;;
  "-dnpo NAME,TYPE") printf '/dev/vda disk\n/dev/vdb disk\n';;
  "-dno TRAN "*) echo virtio;;
  "-nro LABEL /dev/vda") printf '`+labels+`\n';;
  "-nro LABEL /dev/vdb") echo;;
  "-nrpo NAME /dev/vda") printf '`+parts+`\n';;
  "-nrpo NAME /dev/vdb") printf '/dev/vdb\n/dev/vdb1\n';;
  "-nrpo NAME,PARTLABEL /dev/vda") printf '`+partlabels+`\n';;
  "-nrpo NAME,PARTLABEL /dev/vdb") printf '/dev/vdb \n/dev/vdb1 \n';;
esac`)
	shim(t, bin, "blockdev", `case "${@: -1}" in /dev/vda) echo 8589934592;; /dev/vdb) echo 17179869184;; *) echo 0;; esac`)
	shim(t, bin, "mdadm", `[ "${@: -1}" = /dev/vdb1 ] && echo "MD_NAME=vm:data"; exit 0`)
	shim(t, bin, "blkid", `case "${@: -1}" in /dev/vda4) echo xfs;; /dev/vdb1) echo linux_raid_member;; esac`)
	shim(t, bin, "findmnt", "exit 1")
	shim(t, bin, "systemd-detect-virt", "exit 1")
	shim(t, bin, "blkdiscard", "exit 0")
	shim(t, bin, "dd", "exit 0")
	for _, n := range []string{"hostnamectl", "hostname"} {
		shim(t, bin, n, "exit 0")
	}
	cmd := exec.Command("bash", filepath.Join("..", "usb", "pre-install.sh"))
	re := ""
	if reinstall {
		re = "1"
	}
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "SHIM_LOG="+logf, "LAIB_SYS="+sys, "LAIB_INSTALLER_D="+inst, "SERVER_NAME=vm", "LAIB_REINSTALL="+re)
	b, err := cmd.CombinedOutput()
	l, _ := os.ReadFile(logf)
	return string(l), string(b), err
}

// #81: a reinstall writes a small CoreOS image over a disk whose root
// partition still holds the old xfs. The first boot grows the new root into
// that region; the old metadata there carries the same meta_uuid (CoreOS
// restamps only the visible UUID) and a higher log sequence, so the log
// recovery after a power cut reads it as "metadata ahead of the log" and the
// root never mounts again (vm-install run 38277249889). The old root is
// zeroed before the image is written.
func TestReinstallZeroesTheOldRootBeforeTheImage(t *testing.T) {
	log, out, err := runPreInstall(t, true, true)
	if err != nil {
		t.Fatalf("pre-install failed: %v\n%s\n%s", err, out, log)
	}
	if !strings.Contains(log, "blkdiscard -f -z /dev/vda4") {
		t.Fatalf("the old root /dev/vda4 was not zeroed:\n%s\n%s", log, out)
	}
	for _, l := range strings.Split(log, "\n") {
		if strings.HasPrefix(l, "blkdiscard") && !strings.HasSuffix(l, "/dev/vda4") {
			t.Fatalf("only the old OS root may be zeroed, not %q", l)
		}
	}
	if !strings.Contains(out, "/dev/vda4") || !strings.Contains(out, "genullt") {
		t.Fatalf("the console should say that the old root is zeroed: %s", out)
	}
}

// An empty OS disk has nothing to zero; the guard against a plain stick on
// an installed box still stops before anything is touched.
func TestPreInstallZeroesNothingElse(t *testing.T) {
	log, out, err := runPreInstall(t, false, true) // the data array exists, the OS disk is empty
	if err != nil {
		t.Fatalf("fresh install: %v\n%s\n%s", err, out, log)
	}
	if strings.Contains(log, "blkdiscard") || strings.Contains(log, "dd ") {
		t.Fatalf("an empty disk was zeroed:\n%s", log)
	}
	log, out, err = runPreInstall(t, true, false)
	if err == nil || !strings.Contains(out, "schon installiert") {
		t.Fatalf("a plain stick must refuse an installed box: %v %s", err, out)
	}
	if strings.Contains(log, "blkdiscard") || strings.Contains(log, "dd ") {
		t.Fatalf("the refused install touched a disk:\n%s", log)
	}
}
