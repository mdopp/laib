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
