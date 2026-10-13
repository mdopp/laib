package stick

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vincent-petithory/dataurl"
)

// fileFromIgnition returns the contents of one storage file in an Ignition.
func fileFromIgnition(t *testing.T, ign []byte, path string) string {
	t.Helper()
	var doc struct {
		Storage struct {
			Files []struct {
				Path     string `json:"path"`
				Contents struct {
					Source string `json:"source"`
				} `json:"contents"`
			} `json:"files"`
		} `json:"storage"`
	}
	json.Unmarshal(ign, &doc)
	for _, f := range doc.Storage.Files {
		if f.Path == path {
			u, err := dataurl.DecodeString(f.Contents.Source)
			if err != nil {
				t.Fatal(err)
			}
			return string(u.Data)
		}
	}
	t.Fatalf("%s not in the ignition", path)
	return ""
}

// shim writes a fake tool that logs its arguments and runs a small script.
func shim(t *testing.T, bin, name, body string) {
	t.Helper()
	p := filepath.Join(bin, name)
	if err := os.WriteFile(p, []byte("#!/usr/bin/env bash\necho \""+name+" $*\" >> \"$SHIM_LOG\"\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestDataDiskStartsAnInactiveArray: after a boot with a mirror member gone,
// udev's incremental assembly leaves the recorded array INACTIVE (it waits
// for the missing member). The script must start it degraded (mdadm --run)
// before mounting — otherwise /mnt/data stays unmounted, laib's state is
// missing and /ready answers 503 (#33's VM run on c654ce4).
func TestDataDiskStartsAnInactiveArray(t *testing.T) {
	ign, err := DestIgnition(box())
	if err != nil {
		t.Fatal(err)
	}
	script := fileFromIgnition(t, ign, "/usr/local/bin/laib-data-disk.sh")
	root := t.TempDir()
	bin, etc, sys, mp := filepath.Join(root, "bin"), filepath.Join(root, "etc"), filepath.Join(root, "sys"), filepath.Join(root, "mnt")
	for _, d := range []string{bin, filepath.Join(etc, "laib"), filepath.Join(etc, "systemd/system"), filepath.Join(sys, "block/md127/md"), mp} {
		os.MkdirAll(d, 0o755)
	}
	os.WriteFile(filepath.Join(etc, "laib/data-disk"), []byte("DEVICE=/dev/md127 UUID=aaaa:bbbb MEMBERS=2\n"), 0o644)
	os.WriteFile(filepath.Join(sys, "block/md127/md/array_state"), []byte("inactive\n"), 0o644)
	log := filepath.Join(root, "shim.log")
	// the array is known to the kernel (scan lists it) but inactive until --run
	shim(t, bin, "mdadm", `case "$*" in
  "--detail --scan") echo "ARRAY /dev/md127 metadata=1.2 name=vm:data UUID=aaaa:bbbb";;
  "--run /dev/md127") echo active > "$LAIB_SYS/block/md127/md/array_state";;
  "--detail /dev/md127") grep -q active "$LAIB_SYS/block/md127/md/array_state" && echo "     Active Devices : 1" || { echo "mdadm: md device /dev/md127 does not appear to be active." >&2; exit 1; };;
esac`)
	shim(t, bin, "blkid", `grep -q active "$LAIB_SYS/block/md127/md/array_state" || exit 2; echo 1234-fs-uuid`)
	shim(t, bin, "mount", `grep -q active "$LAIB_SYS/block/md127/md/array_state" || { echo "mount: can't read superblock" >&2; exit 32; }; echo mounted > "$LAIB_MP/.mounted"`)
	shim(t, bin, "findmnt", `test -f "$LAIB_MP/.mounted"`)
	shim(t, bin, "readlink", `echo "${@: -1}"`)
	for _, n := range []string{"systemctl", "chattr", "chcon", "chown", "udevadm", "sleep"} {
		shim(t, bin, n, "exit 0")
	}
	sh := filepath.Join(root, "laib-data-disk.sh")
	os.WriteFile(sh, []byte(script), 0o755)
	cmd := exec.Command("bash", sh)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "SHIM_LOG="+log, "LAIB_ETC="+etc, "LAIB_SYS="+sys, "LAIB_MP="+mp)
	out, err := cmd.CombinedOutput()
	logged, _ := os.ReadFile(log)
	if err != nil {
		t.Fatalf("script failed: %v\n%s\nshims:\n%s", err, out, logged)
	}
	if !strings.Contains(string(logged), "mdadm --run /dev/md127") {
		t.Fatalf("the inactive array was never started:\n%s\n%s", logged, out)
	}
	if _, err := os.Stat(filepath.Join(mp, ".mounted")); err != nil {
		t.Fatalf("not mounted:\n%s\n%s", logged, out)
	}
	if !strings.Contains(string(out), "1 of 2 member(s) active") {
		t.Fatalf("the log line should say 1 of 2 members: %s", out)
	}
}

// The other face of the same race: udev has already added the member, the
// array sits INACTIVE in /proc/mdstat, `mdadm --detail --scan` does not list
// it and `--assemble` fails because the member is busy. The script must
// start the inactive array it sees in /proc/mdstat and carry on.
func TestDataDiskStartsAnArrayUdevHoldsInactive(t *testing.T) {
	ign, err := DestIgnition(box())
	if err != nil {
		t.Fatal(err)
	}
	script := fileFromIgnition(t, ign, "/usr/local/bin/laib-data-disk.sh")
	root := t.TempDir()
	bin, etc, sys, mp := filepath.Join(root, "bin"), filepath.Join(root, "etc"), filepath.Join(root, "sys"), filepath.Join(root, "mnt")
	for _, d := range []string{bin, filepath.Join(etc, "laib"), filepath.Join(etc, "systemd/system"), filepath.Join(sys, "block/md127/md"), mp} {
		os.MkdirAll(d, 0o755)
	}
	os.WriteFile(filepath.Join(etc, "laib/data-disk"), []byte("DEVICE=/dev/md127 UUID=aaaa:bbbb MEMBERS=2\n"), 0o644)
	os.WriteFile(filepath.Join(sys, "block/md127/md/array_state"), []byte("inactive\n"), 0o644)
	mdstat := filepath.Join(root, "mdstat")
	os.WriteFile(mdstat, []byte("Personalities : [raid1]\nmd127 : inactive vdb1[0](S)\n      9426880 blocks super 1.2\n\nunused devices: <none>\n"), 0o644)
	log := filepath.Join(root, "shim.log")
	shim(t, bin, "mdadm", `case "$*" in
  "--detail --scan") grep -q active "$LAIB_SYS/block/md127/md/array_state" && echo "ARRAY /dev/md127 metadata=1.2 name=vm:data UUID=aaaa:bbbb";;
  "--run /dev/md127") echo active > "$LAIB_SYS/block/md127/md/array_state"; printf 'Personalities : [raid1]\nmd127 : active raid1 vdb1[0]\n      9426880 blocks super 1.2 [2/1] [U_]\n' > "$LAIB_MDSTAT";;
  --assemble*) echo "mdadm: /dev/vdb1 is busy - skipping" >&2; exit 1;;
  "--detail /dev/md127") grep -q active "$LAIB_SYS/block/md127/md/array_state" && echo "     Active Devices : 1" || exit 1;;
esac`)
	shim(t, bin, "blkid", `grep -q active "$LAIB_SYS/block/md127/md/array_state" || exit 2; echo 1234-fs-uuid`)
	shim(t, bin, "mount", `grep -q active "$LAIB_SYS/block/md127/md/array_state" || exit 32; echo mounted > "$LAIB_MP/.mounted"`)
	shim(t, bin, "findmnt", `test -f "$LAIB_MP/.mounted"`)
	shim(t, bin, "readlink", `echo "${@: -1}"`)
	for _, n := range []string{"systemctl", "chattr", "chcon", "chown", "udevadm", "sleep"} {
		shim(t, bin, n, "exit 0")
	}
	sh := filepath.Join(root, "laib-data-disk.sh")
	os.WriteFile(sh, []byte(script), 0o755)
	cmd := exec.Command("bash", sh)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "SHIM_LOG="+log, "LAIB_ETC="+etc, "LAIB_SYS="+sys, "LAIB_MP="+mp, "LAIB_MDSTAT="+mdstat)
	out, err := cmd.CombinedOutput()
	logged, _ := os.ReadFile(log)
	if err != nil {
		t.Fatalf("script failed: %v\n%s\nshims:\n%s", err, out, logged)
	}
	if !strings.Contains(string(logged), "mdadm --run /dev/md127") || !strings.Contains(string(out), "1 of 2 member(s) active") {
		t.Fatalf("the udev-held array was not started:\n%s\n%s", logged, out)
	}
}

// unitFromIgnition returns the contents of one systemd unit in an Ignition.
func unitFromIgnition(t *testing.T, ign []byte, name string) string {
	t.Helper()
	var doc struct {
		Systemd struct {
			Units []struct {
				Name     string `json:"name"`
				Contents string `json:"contents"`
			} `json:"units"`
		} `json:"systemd"`
	}
	json.Unmarshal(ign, &doc)
	for _, u := range doc.Systemd.Units {
		if u.Name == name {
			return u.Contents
		}
	}
	t.Fatalf("%s not in the ignition", name)
	return ""
}

// The data-disk service lives in /usr/local/bin, which is /var/usrlocal on
// CoreOS — a mount of its own. Ordered After=local-fs.target while the mount
// it enables sits Before= that target, systemd found an ordering cycle,
// dropped local-fs.target from the transaction and sometimes spawned the
// service before /var was there: "Unable to locate executable
// /usr/local/bin/laib-data-disk.sh" (203/EXEC), no /mnt/data, laib never
// ready (VM runs on #63/#64, fix/33 9647ff1). The unit must wait for the
// mount that holds it, not for a target the mount it creates belongs to.
func TestDataDiskUnitWaitsForItsOwnScriptNotForLocalFs(t *testing.T) {
	ign, err := DestIgnition(box())
	if err != nil {
		t.Fatal(err)
	}
	unit := unitFromIgnition(t, ign, "laib-data-disk.service")
	for _, line := range strings.Split(unit, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "After=") && strings.Contains(line, "local-fs.target") {
			t.Fatalf("the data-disk unit orders itself after local-fs.target, which its own mount is part of (an ordering cycle):\n%s", unit)
		}
	}
	if !strings.Contains(unit, "RequiresMountsFor=/var/usrlocal/bin") {
		t.Fatalf("the data-disk unit does not wait for the mount its script lives on:\n%s", unit)
	}
	script := fileFromIgnition(t, ign, "/usr/local/bin/laib-data-disk.sh")
	if strings.Contains(script, "After=local-fs.target laib-data-disk.service") {
		t.Fatal("the mount unit the script writes orders itself after local-fs.target, which it is part of")
	}
}

// #107: after a cut early in a reinstall's first boot laib-data-disk failed,
// and laib-firstboot — which only wanted it — initialised the state repo in
// /mnt/data on the OS disk and reported bereit (run 38355641379). The house's
// data would have landed on the disk the next reinstall wipes. First boot
// requires its data disk; the box without one exits 0 there (DEVICE=none).
func TestFirstBootNeverRunsWithoutItsDataDisk(t *testing.T) {
	ign, err := DestIgnition(box())
	if err != nil {
		t.Fatal(err)
	}
	unit := unitFromIgnition(t, ign, "laib-firstboot.service")
	var requires bool
	for _, line := range strings.Split(unit, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Requires=") && strings.Contains(line, "laib-data-disk.service") {
			requires = true
		}
	}
	if !requires {
		t.Fatalf("laib-firstboot runs when the data disk failed (Wants=, not Requires=):\n%s", unit)
	}
}

// #107: a cut seconds after the first boot adopted the array left
// /etc/laib/data-disk and /etc/mdadm.conf at 0 bytes (xfs: the files were
// created, their data never written). The next boot took the empty record
// for a pre-#32 one, `uuid=$(mdadm --detail --export "")` failed under
// set -e and the script died without a word (run 38361772867). An empty
// record is no record: adopt Laib's array again, and write the record so
// it survives a cut (fsync before it counts).
func TestAnEmptyRecordAfterACutAdoptsTheArrayAgain(t *testing.T) {
	ign, err := DestIgnition(box())
	if err != nil {
		t.Fatal(err)
	}
	script := fileFromIgnition(t, ign, "/usr/local/bin/laib-data-disk.sh")
	root := t.TempDir()
	bin, etc, sys, mp := filepath.Join(root, "bin"), filepath.Join(root, "etc"), filepath.Join(root, "sys"), filepath.Join(root, "mnt")
	for _, d := range []string{bin, filepath.Join(etc, "laib"), filepath.Join(etc, "systemd/system"), filepath.Join(sys, "block/md127/md"), mp} {
		os.MkdirAll(d, 0o755)
	}
	rec := filepath.Join(etc, "laib/data-disk")
	os.WriteFile(rec, nil, 0o644)
	os.WriteFile(filepath.Join(etc, "mdadm.conf"), nil, 0o644)
	os.WriteFile(filepath.Join(sys, "block/md127/md/array_state"), []byte("clean\n"), 0o644)
	log := filepath.Join(root, "shim.log")
	shim(t, bin, "mdadm", `case "$*" in
  "--detail --scan") echo "ARRAY /dev/md127 metadata=1.2 name=vm:data UUID=aaaa:bbbb";;
  "--examine --export /dev/vdb1") printf 'MD_LEVEL=raid1\nMD_NAME=vm:data\nMD_UUID=aaaa:bbbb\n';;
  "--detail /dev/md127") echo "     Active Devices : 2";;
  *) exit 1;;
esac`)
	shim(t, bin, "lsblk", `case "$*" in
  "/dev/vda4") exit 0;;
  "-ndo PKNAME /dev/vda4") echo vda;;
  "-nrpo NAME,TYPE") printf '/dev/vda disk\n/dev/vda4 part\n/dev/vdb disk\n/dev/vdb1 part\n/dev/md127 raid1\n';;
esac`)
	shim(t, bin, "findmnt", `case "$*" in "-n -o SOURCE /") echo /dev/vda4;; *) test -f "$LAIB_MP/.mounted";; esac`)
	shim(t, bin, "blkid", `case "$*" in *LABEL*) echo data;; *) echo 1234-fs-uuid;; esac`)
	shim(t, bin, "mount", `echo mounted > "$LAIB_MP/.mounted"`)
	shim(t, bin, "readlink", `echo "${@: -1}"`)
	for _, n := range []string{"systemctl", "chattr", "chcon", "chown", "udevadm", "sleep", "sync"} {
		shim(t, bin, n, "exit 0")
	}
	sh := filepath.Join(root, "laib-data-disk.sh")
	os.WriteFile(sh, []byte(script), 0o755)
	cmd := exec.Command("bash", sh)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "SHIM_LOG="+log, "LAIB_ETC="+etc, "LAIB_SYS="+sys, "LAIB_MP="+mp)
	out, err := cmd.CombinedOutput()
	logged, _ := os.ReadFile(log)
	if err != nil {
		t.Fatalf("the empty record killed the script: %v\n%s\nshims:\n%s", err, out, logged)
	}
	if _, err := os.Stat(filepath.Join(mp, ".mounted")); err != nil {
		t.Fatalf("the array was not mounted:\n%s\n%s", out, logged)
	}
	if b, _ := os.ReadFile(rec); !strings.Contains(string(b), "UUID=aaaa:bbbb") {
		t.Fatalf("the record was not written again: %q\n%s", b, out)
	}
	if !strings.Contains(string(logged), "sync "+rec) {
		t.Fatalf("the record is not synced before it counts:\n%s", logged)
	}
}
