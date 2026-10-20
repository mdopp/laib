package disk

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #488: the Kraftwerk's data array was made by ServiceBay, so laib-data-disk.sh
// never wrote /etc/laib/data-disk. Without that record laib-disk said "keine
// Datenplatte" and diag said nothing about the degraded mirror. The array
// /mnt/data is mounted from is the data array, record or not.

const mdstatKraftwerk = `Personalities : [raid1]
md127 : active raid1 nvme0n1p1[0]
      1953381440 blocks super 1.2 [2/1] [U_]
      bitmap: 8/15 pages [32KB], 65536KB chunk

unused devices: <none>
`

const mountsKraftwerk = `/dev/nvme1n1p4 /sysroot xfs ro,relatime 0 0
/dev/nvme1n1p3 /boot ext4 ro,nosuid,nodev,relatime 0 0
/dev/md127 /var/mnt/data xfs rw,noatime 0 0
`

// noMarker points laib-disk at a box without Laib's record.
func noMarker(t *testing.T, mdstat, mounts string) {
	t.Helper()
	dir := t.TempDir()
	oldMarker, oldMd, oldMounts := dataDiskMarker, mdstatPath, mountsPath
	t.Cleanup(func() { dataDiskMarker, mdstatPath, mountsPath = oldMarker, oldMd, oldMounts })
	dataDiskMarker = filepath.Join(dir, "data-disk")
	mdstatPath = filepath.Join(dir, "mdstat")
	mountsPath = filepath.Join(dir, "mounts")
	os.WriteFile(mdstatPath, []byte(mdstat), 0o644)
	os.WriteFile(mountsPath, []byte(mounts), 0o644)
}

func TestTheDataArrayWithoutLaibsRecord(t *testing.T) {
	noMarker(t, mdstatKraftwerk, mountsKraftwerk)
	if dev, members := DataArray(); dev != "/dev/md127" || members != 0 {
		t.Fatalf("data array %q, %d members recorded; want /dev/md127 from the mount, none recorded", dev, members)
	}
	f := Probe(context.Background(), false)
	if len(f) != 1 || f[0].Severity != "gelb" || !strings.Contains(f[0].Detail, "Spiegel fehlt") || !strings.Contains(f[0].Fix, "laib disk add") || !strings.Contains(f[0].Fix, "mdadm --detail /dev/md127") {
		t.Fatalf("a degraded data array without a record: %+v", f)
	}
	var out bytes.Buffer
	if err := Cmd(context.Background(), &out, "human:x", false, []string{"status"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "keine Datenplatte") || !strings.Contains(out.String(), "Datenplatte: /dev/md127") {
		t.Fatalf("status:\n%s", out.String())
	}
}

func TestNoDataDiskWhenMntDataIsOnTheOSDisk(t *testing.T) {
	noMarker(t, mdstatKraftwerk, "/dev/nvme1n1p4 /sysroot xfs ro 0 0\n")
	if f := Probe(context.Background(), false); len(f) != 0 {
		t.Fatalf("an array /mnt/data is not on is not the data array: %+v", f)
	}
	var out bytes.Buffer
	Cmd(context.Background(), &out, "human:x", false, []string{"status"})
	if !strings.Contains(out.String(), "keine Datenplatte") {
		t.Fatalf("status:\n%s", out.String())
	}
}

func TestRaidFindingsOfTheDataArray(t *testing.T) {
	failed := "md127 : active raid1 nvme1n1p1[1](F) nvme0n1p1[0]\n      1953381440 blocks super 1.2 [2/1] [U_]\n"
	other := "md0 : active raid1 sda1[0]\n      1000 blocks super 1.2 [2/1] [U_]\n\n" + mdstatTwo
	cases := []struct {
		name, mdstat string
		expected     int
		severity     string
		fix          string
	}{
		{"degraded, not recorded", mdstatKraftwerk, 0, "gelb", "laib disk add"},
		{"a member marked failed", failed, 0, "rot", "laib disk add"},
		{"rebuilding", mdstatSync, 0, "gelb", "laib disk status"},
		{"another array is not the data array", other, 2, "", ""},
	}
	for _, c := range cases {
		f := raidFindings(c.mdstat, "md127", c.expected)
		got, fix := "", ""
		if len(f) > 0 {
			got, fix = f[0].Severity, f[0].Fix
		}
		if got != c.severity || !strings.Contains(fix, c.fix) {
			t.Errorf("%s: got %q %q (%v), want %q with %q", c.name, got, fix, f, c.severity, c.fix)
		}
	}
}
