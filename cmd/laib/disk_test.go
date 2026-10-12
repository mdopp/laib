package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/sysd"
)

// fakeDisk answers the commands inspectSpare runs, like the real tools would
// for one device.
type fakeDisk struct {
	lsblk    string // lsblk -dnbP -o TYPE,SIZE,RM,TRAN,MODEL
	children string // lsblk -nr -o NAME
	wipefs   string // wipefs -n --noheadings -O TYPE,LABEL
	superblk bool   // mdadm --examine finds one
}

func (f fakeDisk) run(_ context.Context, name string, args ...string) (string, error) {
	cmd := name + " " + strings.Join(args, " ")
	switch {
	case strings.Contains(cmd, "lsblk -dnbP"):
		return f.lsblk, nil
	case strings.Contains(cmd, "lsblk -nr -o NAME"):
		return f.children, nil
	case strings.Contains(cmd, "wipefs -n"):
		return f.wipefs, nil
	case strings.Contains(cmd, "mdadm --examine"):
		if f.superblk {
			return "/dev/x:\n          Magic : a92b4efc", nil
		}
		return "mdadm: No md superblock detected on /dev/x.", errors.New("exit status 1")
	}
	return "", errors.New("unexpected command: " + cmd)
}

// lsblk lists a RAID member's children too: without -d, PKNAME of vdb1 comes
// back as two lines ("vdb" for the partition, "vdb1" for md127 on top of it).
// The VM job caught `laib disk add` asking lsblk for "/dev/vdb\nvdb1" (#32).
func TestMemberDiskBytesAsksForTheParentOnly(t *testing.T) {
	saved := sysd.Run
	defer func() { sysd.Run = saved }()
	sysd.Run = func(_ context.Context, name string, args ...string) (string, error) {
		cmd := name + " " + strings.Join(args, " ")
		switch cmd {
		case "lsblk -no PKNAME /dev/vdb1":
			return "vdb\nvdb1", nil // the partition, then md127 on top of it
		case "lsblk -dno PKNAME /dev/vdb1":
			return "vdb", nil
		case "lsblk -dnbo SIZE /dev/vdb":
			return "9663676416", nil
		}
		return "lsblk: not a block device", errors.New("exit status 32: " + cmd)
	}
	got, err := memberDiskBytes(context.Background(), "vdb1")
	if err != nil || got != 9663676416 {
		t.Fatalf("memberDiskBytes(vdb1) = %d, %v; want the size of vdb", got, err)
	}
}

// The record keeps the array's UUID when the mirror joins: the boot script
// assembles by UUID, and the old marker write dropped it (#32).
func TestWithMembersKeepsTheUUID(t *testing.T) {
	cases := map[string]string{
		"DEVICE=/dev/md127 UUID=1a2b:3c4d:5e6f:7a8b MEMBERS=1\n": "DEVICE=/dev/md127 UUID=1a2b:3c4d:5e6f:7a8b MEMBERS=2",
		"DEVICE=/dev/md127 MEMBERS=1":                            "DEVICE=/dev/md127 MEMBERS=2",
		"DEVICE=/dev/md127 UUID=x":                               "DEVICE=/dev/md127 UUID=x MEMBERS=2",
	}
	for in, want := range cases {
		if got := withMembers(in, 2); got != want {
			t.Errorf("withMembers(%q) = %q, want %q", in, got, want)
		}
	}
}

// `laib disk add` wiped any unmounted device it was given (#32). Now it
// inspects the device first and refuses what it would destroy, unless the
// human names the disk's model with --wipe; a disk smaller than the array's
// is refused either way, before anything is written.
func TestDiskAddRefusesWhatItWouldDestroy(t *testing.T) {
	const qemu = `TYPE="disk" SIZE="9663676416" RM="0" TRAN="" MODEL="QEMU HARDDISK"`
	const min = 9663676416 // the array's disk
	cases := []struct {
		name    string
		disk    fakeDisk
		wipe    string
		wantErr string // "" = accepted
	}{
		{"an empty disk of the same size", fakeDisk{lsblk: qemu, children: "vdc"}, "", ""},
		{"partitions", fakeDisk{lsblk: qemu, children: "vdc\nvdc1\nvdc2"}, "", "2 partition"},
		{"a filesystem", fakeDisk{lsblk: qemu, children: "vdc", wipefs: "ext4 backup"}, "", "ext4 backup"},
		{"an old RAID member", fakeDisk{lsblk: qemu, children: "vdc", wipefs: "linux_raid_member nas:0", superblk: true}, "", "RAID superblock"},
		{"a USB disk", fakeDisk{lsblk: `TYPE="disk" SIZE="2000398934016" RM="0" TRAN="usb" MODEL="WD Elements 25A2"`, children: "sdd"}, "", "USB"},
		{"a removable disk", fakeDisk{lsblk: `TYPE="disk" SIZE="31914983424" RM="1" TRAN="usb" MODEL="SanDisk 3.2Gen1"`, children: "sde"}, "", "removable"},
		{"a partition, not a disk", fakeDisk{lsblk: `TYPE="part" SIZE="9663676416" RM="0" TRAN="" MODEL=""`, children: "vdc1"}, "", "not a whole disk"},
		{"too small", fakeDisk{lsblk: `TYPE="disk" SIZE="1073741824" RM="0" TRAN="sata" MODEL="small"`, children: "sdx"}, "", "smaller"},
		{"a filesystem, confirmed by model", fakeDisk{lsblk: qemu, children: "vdc", wipefs: "ext4 backup"}, "qemu harddisk", ""},
		{"a filesystem, the wrong model named", fakeDisk{lsblk: qemu, children: "vdc", wipefs: "ext4 backup"}, "WD Elements", "not empty"},
		{"too small, even when confirmed", fakeDisk{lsblk: `TYPE="disk" SIZE="1073741824" RM="0" TRAN="sata" MODEL="small"`, children: "sdx"}, "small", "smaller"},
	}
	saved := sysd.Run
	defer func() { sysd.Run = saved }()
	for _, c := range cases {
		sysd.Run = c.disk.run
		s, err := inspectSpare(context.Background(), "/dev/vdc")
		if err == nil {
			err = s.refuse(min, c.wipe)
		}
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("%s: refused: %v", c.name, err)
		case c.wantErr != "" && err == nil:
			t.Errorf("%s: accepted, want a refusal mentioning %q", c.name, c.wantErr)
		case c.wantErr != "" && !strings.Contains(err.Error(), c.wantErr):
			t.Errorf("%s: refusal %q does not mention %q", c.name, err, c.wantErr)
		}
	}
}

const mdstatOne = `Personalities : [raid1]
md127 : active raid1 vdb1[0]
      16760832 blocks super 1.2 [2/1] [U_]

unused devices: <none>
`

const mdstatTwo = `Personalities : [raid1]
md127 : active raid1 vdc1[2] vdb1[0]
      16760832 blocks super 1.2 [2/2] [UU]
`

const mdstatSync = `Personalities : [raid1]
md127 : active raid1 vdc1[2] vdb1[0]
      16760832 blocks super 1.2 [2/1] [U_]
      [===>.................]  recovery = 17.4% (2920000/16760832) finish=1.1min speed=200000K/sec
`

func TestParseMdstat(t *testing.T) {
	a := parseMdstat(mdstatTwo)
	if len(a) != 1 || a[0].Name != "md127" || a[0].Slots != 2 || a[0].Active != 2 || a[0].Status != "UU" || len(a[0].Members) != 2 {
		t.Fatalf("parsed %+v", a)
	}
	if s := parseMdstat(mdstatSync); s[0].Resync != "recovery 17.4%" {
		t.Fatalf("resync %q", s[0].Resync)
	}
}

func TestRaidFindings(t *testing.T) {
	cases := []struct {
		name     string
		mdstat   string
		expected int
		severity string
	}{
		{"no data disk", "", 0, ""},
		{"one member by design", mdstatOne, 1, "gelb"},
		{"mirror healthy", mdstatTwo, 2, ""},
		{"a member failed", mdstatOne, 2, "rot"},
		{"syncing", mdstatSync, 2, "info"},
		{"array gone", "Personalities : [raid1]\nunused devices: <none>\n", 1, "rot"},
	}
	for _, c := range cases {
		f := raidFindings(c.mdstat, c.expected)
		got := ""
		if len(f) > 0 {
			got = f[0].Severity
		}
		if got != c.severity {
			t.Errorf("%s: got %q (%v), want %q", c.name, got, f, c.severity)
		}
	}
}
