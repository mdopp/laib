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

// diskBox is a scratch root for laib-data-disk.sh: its bin, etc, sys and the
// mount point, with the array's state and (unless nil) the record written.
type diskBox struct{ root, bin, etc, sys, mp, rec, log, script string }

func newDiskBox(t *testing.T, mp, record, state string) *diskBox { // mp "-": no mount point made
	t.Helper()
	ign, err := DestIgnition(box())
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	b := &diskBox{root: root, bin: filepath.Join(root, "bin"), etc: filepath.Join(root, "etc"), sys: filepath.Join(root, "sys"),
		mp: filepath.Join(root, "mnt"), log: filepath.Join(root, "shim.log"), script: fileFromIgnition(t, ign, "/usr/local/bin/laib-data-disk.sh")}
	if mp != "" { // "-": the test makes its own mount point
		b.mp = filepath.Join(root, "mnt", "data")
	}
	b.rec = filepath.Join(b.etc, "laib/data-disk")
	for _, d := range []string{b.bin, filepath.Join(b.etc, "laib"), filepath.Join(b.etc, "systemd/system/multi-user.target.wants"), filepath.Join(b.sys, "block/md127/md")} {
		os.MkdirAll(d, 0o755)
	}
	if mp == "" {
		os.MkdirAll(b.mp, 0o755)
	}
	if record != "-" {
		os.WriteFile(b.rec, []byte(record), 0o644)
	}
	os.WriteFile(filepath.Join(b.sys, "block/md127/md/array_state"), []byte(state+"\n"), 0o644)
	return b
}

// run gives every tool not shimmed yet a no-op, runs the script, and fails
// the test on a non-zero exit; it returns the script's output and the shim log.
func (b *diskBox) run(t *testing.T, env ...string) (string, string) {
	t.Helper()
	for _, n := range []string{"systemctl", "chattr", "chcon", "chown", "udevadm", "sleep", "sync"} {
		if _, err := os.Stat(filepath.Join(b.bin, n)); err != nil {
			shim(t, b.bin, n, "exit 0")
		}
	}
	sh := filepath.Join(b.root, "laib-data-disk.sh")
	os.WriteFile(sh, []byte(b.script), 0o755)
	cmd := exec.Command("bash", sh)
	cmd.Env = append(append(os.Environ(), "PATH="+b.bin+":"+os.Getenv("PATH"), "SHIM_LOG="+b.log, "LAIB_ETC="+b.etc, "LAIB_SYS="+b.sys, "LAIB_MP="+b.mp), env...)
	out, err := cmd.CombinedOutput()
	logged, _ := os.ReadFile(b.log)
	if err != nil {
		t.Fatalf("script failed: %v\n%s\nshims:\n%s", err, out, logged)
	}
	return string(out), string(logged)
}

// TestDataDiskStartsAnInactiveArray: after a boot with a mirror member gone,
// udev's incremental assembly leaves the recorded array INACTIVE (it waits
// for the missing member). The script must start it degraded (mdadm --run)
// before mounting — otherwise /mnt/data stays unmounted, laib's state is
// missing and /ready answers 503 (#33's VM run on c654ce4).
func TestDataDiskStartsAnInactiveArray(t *testing.T) {
	b := newDiskBox(t, "", "DEVICE=/dev/md127 UUID=aaaa:bbbb MEMBERS=2\n", "inactive")
	bin, mp := b.bin, b.mp
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
	out, logged := b.run(t)
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
	b := newDiskBox(t, "", "DEVICE=/dev/md127 UUID=aaaa:bbbb MEMBERS=2\n", "inactive")
	bin, root := b.bin, b.root
	mdstat := filepath.Join(root, "mdstat")
	os.WriteFile(mdstat, []byte("Personalities : [raid1]\nmd127 : inactive vdb1[0](S)\n      9426880 blocks super 1.2\n\nunused devices: <none>\n"), 0o644)
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
	out, logged := b.run(t, "LAIB_MDSTAT="+mdstat)
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
	b := newDiskBox(t, "", "", "clean")
	bin, etc, mp, rec := b.bin, b.etc, b.mp, b.rec
	os.WriteFile(filepath.Join(etc, "mdadm.conf"), nil, 0o644)
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
	out, logged := b.run(t)
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

// #203: on Fedora CoreOS /mnt is a symlink to var/mnt. The mount unit the
// script wrote said Where=/mnt/data, and systemd refuses it on every boot
// ("Mount path /mnt/data is not canonical (contains a symlink)"): a failed
// unit and a degraded system on every box, while the script's own mount did
// the work. No unit the script leaves may name a path behind a symlink, and
// the old mnt-data.mount of an installed box is gone after its next boot.
func TestDataDiskLeavesNoMountUnitSystemdRefuses(t *testing.T) {
	b := newDiskBox(t, "-", "DEVICE=/dev/md127 UUID=aaaa:bbbb MEMBERS=2\n", "clean")
	bin, root, mp := b.bin, b.root, b.mp
	units := filepath.Join(b.etc, "systemd/system")
	os.MkdirAll(filepath.Join(root, "var/mnt/data"), 0o755)
	os.Symlink("var/mnt", filepath.Join(root, "mnt")) // CoreOS: /mnt -> var/mnt
	old := filepath.Join(units, "mnt-data.mount")
	os.WriteFile(old, []byte("[Mount]\nWhere=/mnt/data\n"), 0o644)
	os.Symlink(old, filepath.Join(units, "multi-user.target.wants", "mnt-data.mount"))
	shim(t, bin, "mdadm", `case "$*" in
  "--detail --scan") echo "ARRAY /dev/md127 metadata=1.2 name=vm:data UUID=aaaa:bbbb";;
  "--detail /dev/md127") echo "     Active Devices : 2";;
  *) exit 1;;
esac`)
	shim(t, bin, "blkid", `echo 1234-fs-uuid`)
	shim(t, bin, "mount", `echo mounted > "$LAIB_MP/.mounted"`)
	shim(t, bin, "findmnt", `test -f "$LAIB_MP/.mounted"`)
	shim(t, bin, "readlink", `echo "${@: -1}"`)
	shim(t, bin, "systemctl", `case "$*" in "disable mnt-data.mount") rm -f "$LAIB_ETC/systemd/system/multi-user.target.wants/mnt-data.mount";; esac; exit 0`)
	out, logged := b.run(t)
	if _, err := os.Stat(filepath.Join(mp, ".mounted")); err != nil {
		t.Fatalf("the array was not mounted:\n%s\n%s", out, logged)
	}
	matches, _ := filepath.Glob(filepath.Join(units, "*.mount"))
	for _, m := range matches {
		b, _ := os.ReadFile(m)
		for _, l := range strings.Split(string(b), "\n") {
			if w, ok := strings.CutPrefix(l, "Where="); ok {
				if real, _ := filepath.EvalSymlinks(w); real != w {
					t.Errorf("%s: Where=%s is behind a symlink (%s): systemd refuses it on every boot", filepath.Base(m), w, real)
				}
			}
		}
	}
	if _, err := os.Lstat(filepath.Join(units, "multi-user.target.wants", "mnt-data.mount")); err == nil {
		t.Error("the old mnt-data.mount is still enabled")
	}
	if b, err := os.ReadFile(old); err == nil && strings.Contains(string(b), "Where=/mnt/data") {
		t.Error("the old mnt-data.mount (Where=/mnt/data) is still there")
	}
}

// #304: a reinstall's first boot adopts Laib's array while a member that
// fell behind in a power cut is left out of it. The record took the active
// count (MEMBERS=1), and from then on every boot said "1 of 1 member(s)
// active": the box ran on one disk for good, the re-add never ran (run
// 39030051673). What belongs to the array is what carries its UUID; that
// member goes back in on the same boot.
func TestAReinstallAdoptsTheMirrorWithTheMemberThatFellBehind(t *testing.T) {
	b := newDiskBox(t, "", "-", "clean")
	bin, rec := b.bin, b.rec
	// vdc1 is in the array, vdb1 carries its UUID but was left out
	shim(t, bin, "mdadm", `case "$*" in
  "--detail --scan") echo "ARRAY /dev/md127 metadata=1.2 name=vm:data UUID=aaaa:bbbb";;
  "--examine --export /dev/vdb1"|"--examine --export /dev/vdc1") printf 'MD_LEVEL=raid1\nMD_NAME=vm:data\nMD_UUID=aaaa:bbbb\n';;
  "--detail /dev/md127") printf '     Active Devices : 1\n       2     252       33        1      active sync   /dev/vdc1\n';;
  "--manage /dev/md127 --re-add /dev/vdb1") exit 0;;
  *) exit 1;;
esac`)
	shim(t, bin, "lsblk", `case "$*" in
  "/dev/vda4") exit 0;;
  "-ndo PKNAME /dev/vda4") echo vda;;
  "-nrpo NAME,TYPE") printf '/dev/vda disk\n/dev/vda4 part\n/dev/vdb disk\n/dev/vdb1 part\n/dev/vdc disk\n/dev/vdc1 part\n/dev/md127 raid1\n';;
esac`)
	shim(t, bin, "findmnt", `case "$*" in "-n -o SOURCE /") echo /dev/vda4;; *) test -f "$LAIB_MP/.mounted";; esac`)
	shim(t, bin, "blkid", `case "$*" in *LABEL*) echo data;; *) echo 1234-fs-uuid;; esac`)
	shim(t, bin, "mount", `echo mounted > "$LAIB_MP/.mounted"`)
	shim(t, bin, "readlink", `echo "${@: -1}"`)
	out, logged := b.run(t)
	if b, _ := os.ReadFile(rec); !strings.Contains(string(b), "MEMBERS=2") {
		t.Fatalf("the record counts the active members, not the array's: %q\n%s", b, out)
	}
	if !strings.Contains(string(logged), "mdadm --manage /dev/md127 --re-add /dev/vdb1") {
		t.Fatalf("the member that fell behind was not added back:\n%s\n%s", logged, out)
	}
}
