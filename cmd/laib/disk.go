package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/mdopp/laib/internal/sysd"
)

// The data disk (usb/fedora-coreos.bu laib-data-disk.sh, #29): /mnt/data is a
// RAID1 with two slots. Fresh boxes run with one member on purpose; `laib disk
// add` brings the mirror. The diag probe tells "one member by design" from
// "a member is gone".

const dataDiskMarker = "/etc/laib/data-disk" // DEVICE=/dev/md127 MEMBERS=1, written by laib-data-disk.sh

// mdArray is one line pair of /proc/mdstat.
type mdArray struct {
	Name    string // md127
	Level   string // raid1
	Slots   int    // [2/1] → 2
	Active  int    // [2/1] → 1
	Status  string // U_ or UU
	Resync  string // "recovery = 12.3%" when syncing
	Members []string
}

var (
	mdHead  = regexp.MustCompile(`^(md\d+) : (\w+) (raid\d+) (.*)$`)
	mdState = regexp.MustCompile(`\[(\d+)/(\d+)\] \[([U_]+)\]`)
	mdSync  = regexp.MustCompile(`(resync|recovery) = *([\d.]+%)`)
)

// parseMdstat reads the arrays out of /proc/mdstat's text.
func parseMdstat(text string) []mdArray {
	var out []mdArray
	var cur *mdArray
	for _, line := range strings.Split(text, "\n") {
		if m := mdHead.FindStringSubmatch(line); m != nil {
			out = append(out, mdArray{Name: m[1], Level: m[3]})
			cur = &out[len(out)-1]
			for _, f := range strings.Fields(m[4]) {
				if i := strings.IndexByte(f, '['); i > 0 {
					cur.Members = append(cur.Members, f[:i])
				}
			}
			continue
		}
		if cur == nil {
			continue
		}
		if m := mdState.FindStringSubmatch(line); m != nil {
			cur.Slots, _ = strconv.Atoi(m[1])
			cur.Active, _ = strconv.Atoi(m[2])
			cur.Status = m[3]
		}
		if m := mdSync.FindStringSubmatch(line); m != nil {
			cur.Resync = m[1] + " " + m[2]
		}
	}
	return out
}

// markerMembers reads the member count the data-disk script recorded.
func markerMembers() (device string, members int) {
	b, err := os.ReadFile(dataDiskMarker)
	if err != nil {
		return "", 0
	}
	for _, f := range strings.Fields(string(b)) {
		if v, ok := strings.CutPrefix(f, "DEVICE="); ok {
			device = v
		}
		if v, ok := strings.CutPrefix(f, "MEMBERS="); ok {
			members, _ = strconv.Atoi(v)
		}
	}
	return
}

// raidFindings is the diag probe: red when a member that was there is gone
// (or the array is missing), yellow when the mirror was never added.
func raidFindings(mdstat string, expectedMembers int) []Finding {
	arrays := parseMdstat(mdstat)
	if expectedMembers == 0 {
		return nil // no data disk on this box: nothing to say
	}
	if len(arrays) == 0 {
		return []Finding{{Severity: "rot", Probe: "raid", Detail: "/mnt/data: kein RAID aktiv, aber die Box hat eine Datenplatte", Fix: "journalctl -u laib-data-disk"}}
	}
	var out []Finding
	for _, a := range arrays {
		switch {
		case a.Resync != "":
			out = append(out, Finding{Severity: "info", Probe: "raid", Detail: fmt.Sprintf("%s: Spiegel synchronisiert (%s)", a.Name, a.Resync)})
		case a.Active < a.Slots && a.Active >= expectedMembers:
			out = append(out, Finding{Severity: "gelb", Probe: "raid", Detail: fmt.Sprintf("%s: %d von %d Platten, kein Spiegel — gewollt, bis eine zweite Platte kommt", a.Name, a.Active, a.Slots), Fix: "laib disk add /dev/sdX"})
		case a.Active < expectedMembers:
			out = append(out, Finding{Severity: "rot", Probe: "raid", Detail: fmt.Sprintf("%s: nur %d von %d Platten, eine ist ausgefallen (war %d)", a.Name, a.Active, a.Slots, expectedMembers), Fix: "cat /proc/mdstat; Platte tauschen, dann laib disk add"})
		}
	}
	return out
}

// spareInfo is what `laib disk add` learns about a device before it wipes it.
type spareInfo struct {
	Dev, Model, Tran string
	Size             int64
	Removable        bool
	Partitions       int
	Signatures       []string // wipefs -n: "ext4 backup", "gpt", "linux_raid_member nas:0"
	Superblock       bool
}

var lsblkPair = regexp.MustCompile(`(\w+)="([^"]*)"`)

// inspectSpare reads the device the human named, without changing it.
func inspectSpare(ctx context.Context, dev string) (spareInfo, error) {
	s := spareInfo{Dev: dev}
	out, err := sysd.Run(ctx, "lsblk", "-dnbP", "-o", "TYPE,SIZE,RM,TRAN,MODEL", dev)
	if err != nil {
		return s, fmt.Errorf("lsblk %s: %v: %s", dev, err, out)
	}
	kv := map[string]string{}
	for _, m := range lsblkPair.FindAllStringSubmatch(out, -1) {
		kv[m[1]] = m[2]
	}
	if kv["TYPE"] != "disk" {
		return s, fmt.Errorf("%s is a %q, not a whole disk", dev, kv["TYPE"])
	}
	s.Size, _ = strconv.ParseInt(kv["SIZE"], 10, 64)
	s.Removable, s.Tran, s.Model = kv["RM"] == "1", kv["TRAN"], strings.TrimSpace(kv["MODEL"])
	if out, err := sysd.Run(ctx, "lsblk", "-nr", "-o", "NAME", dev); err == nil {
		if n := len(strings.Fields(out)); n > 1 {
			s.Partitions = n - 1
		}
	}
	out, err = sysd.Run(ctx, "sudo", "wipefs", "-n", "--noheadings", "-O", "TYPE,LABEL", dev)
	if err != nil {
		return s, fmt.Errorf("wipefs -n %s: %v: %s", dev, err, out)
	}
	for _, line := range strings.Split(out, "\n") {
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			s.Signatures = append(s.Signatures, line)
		}
	}
	if _, err := sysd.Run(ctx, "sudo", "mdadm", "--examine", dev); err == nil {
		s.Superblock = true
	}
	return s, nil
}

// refuse says why the device must not be wiped, or nil. minBytes is the size
// of the disk that holds the array's current member: a smaller disk cannot
// join, so it is refused even when the human confirms. Content (partitions,
// signatures, a RAID superblock) and USB or removable disks need the human to
// name the disk's model with --wipe (#32).
func (s spareInfo) refuse(minBytes int64, wipe string) error {
	if s.Size < minBytes {
		return fmt.Errorf("%s is smaller than the array's disk (%d < %d bytes): it cannot join. Nothing was changed", s.Dev, s.Size, minBytes)
	}
	var why []string
	if s.Tran == "usb" {
		why = append(why, "it is a USB disk")
	}
	if s.Removable {
		why = append(why, "it is removable")
	}
	if s.Partitions > 0 {
		why = append(why, fmt.Sprintf("it has %d partition(s)", s.Partitions))
	}
	for _, sig := range s.Signatures {
		why = append(why, "it carries "+sig)
	}
	if s.Superblock {
		why = append(why, "it carries a RAID superblock")
	}
	if len(why) == 0 {
		return nil
	}
	name := s.Model
	if name == "" {
		name = s.Dev
	}
	if wipe != "" && strings.EqualFold(strings.TrimSpace(wipe), name) {
		return nil
	}
	return fmt.Errorf("%s (%s, %.1f GB) is not empty: %s. Nothing was changed. If everything on it may go: laib disk add %s --wipe %q",
		s.Dev, name, float64(s.Size)/1e9, strings.Join(why, "; "), s.Dev, name)
}

// memberDiskBytes is the size of the whole disk that holds an array member
// (vdb1 → vdb): the least a new mirror disk needs.
func memberDiskBytes(ctx context.Context, member string) (int64, error) {
	// -d: without it lsblk lists the member's children too (md127), one PKNAME per line
	parent, err := sysd.Run(ctx, "lsblk", "-dno", "PKNAME", "/dev/"+member)
	parent, _, _ = strings.Cut(strings.TrimSpace(parent), "\n")
	if err != nil || parent == "" {
		parent = member // the member is a whole disk
	}
	out, err := sysd.Run(ctx, "lsblk", "-dnbo", "SIZE", "/dev/"+strings.TrimSpace(parent))
	if err != nil {
		return 0, fmt.Errorf("lsblk %s: %v: %s", parent, err, out)
	}
	return strconv.ParseInt(strings.TrimSpace(out), 10, 64)
}

// cmdDisk: status | add <device> [--wipe <model>] (humans only: sudo,
// irreversible for the device's content). add inspects the device and refuses
// what it would destroy (spareInfo.refuse), then partitions it, joins it to
// /dev/md/data and records the new member count.
func cmdDisk(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) == 0 || args[0] == "status" {
		b, err := os.ReadFile("/proc/mdstat")
		if err != nil {
			return fail(err)
		}
		dev, members := markerMembers()
		if env.json {
			return emit(env, map[string]any{"arrays": parseMdstat(string(b)), "device": dev, "members": members})
		}
		for _, a := range parseMdstat(string(b)) {
			fmt.Fprintf(env.w(), "%s %s %d/%d [%s] %s %s\n", a.Name, a.Level, a.Active, a.Slots, a.Status, strings.Join(a.Members, ","), a.Resync)
		}
		if members == 0 {
			fmt.Fprintln(env.w(), "keine Datenplatte: /mnt/data liegt auf der OS-Platte")
		}
		return 0
	}
	wipe := ""
	if len(args) == 4 && args[2] == "--wipe" {
		wipe = args[3]
		args = args[:2]
	}
	if args[0] != "add" || len(args) != 2 {
		return fail(errors.New("usage: laib disk status | add <device, e.g. /dev/sdb> [--wipe <model, when the disk is not empty>]"))
	}
	if !strings.HasPrefix(env.actor, "human") {
		return fail(errors.New("disk add: a human runs this (it wipes the device)"))
	}
	dev := args[1]
	dataDev, members := markerMembers()
	if dataDev == "" || dataDev == "none" {
		return fail(errors.New("no data RAID on this box (/etc/laib/data-disk)"))
	}
	if members >= 2 {
		return fail(errors.New("the array already has two members"))
	}
	b, err := os.ReadFile("/proc/mdstat")
	if err != nil {
		return fail(err)
	}
	arrays := parseMdstat(string(b))
	if len(arrays) == 0 || len(arrays[0].Members) == 0 {
		return fail(errors.New("no active array in /proc/mdstat"))
	}
	minBytes, err := memberDiskBytes(ctx, arrays[0].Members[0])
	if err != nil {
		return fail(err)
	}
	spare, err := inspectSpare(ctx, dev)
	if err != nil {
		return fail(err)
	}
	if err := spare.refuse(minBytes, wipe); err != nil {
		return fail(err)
	}
	steps := [][]string{
		{"sudo", "wipefs", "-a", dev},
		{"sudo", "sgdisk", "-Z", "-n", "1:0:0", "-t", "1:fd00", "-c", "1:laib-data", dev},
		{"sudo", "udevadm", "settle"},
	}
	for _, s := range steps {
		if out, err := sysd.Run(ctx, s[0], s[1:]...); err != nil {
			return fail(fmt.Errorf("%s: %v: %s", strings.Join(s, " "), err, out))
		}
	}
	part := dev + "1"
	if _, err := os.Stat(part); err != nil {
		part = dev + "p1"
	}
	// the recorded device, not /dev/md/data: after a reinstall under another
	// hostname the array may carry a different name
	if out, err := sysd.Run(ctx, "sudo", "mdadm", "--manage", dataDev, "--add", part); err != nil {
		return fail(fmt.Errorf("mdadm --add: %v: %s", err, out))
	}
	rec, err := os.ReadFile(dataDiskMarker)
	if err != nil {
		return fail(err)
	}
	if out, err := sysd.Run(ctx, "sudo", "sh", "-c", `printf '%s\n' "$0" > `+dataDiskMarker, withMembers(string(rec), 2)); err != nil {
		return fail(fmt.Errorf("marker: %v: %s", err, out))
	}
	fmt.Fprintf(env.w(), "%s joined %s; it syncs now — laib disk status shows the progress\n", part, dataDev)
	return 0
}

var membersField = regexp.MustCompile(`MEMBERS=\d+`)

// withMembers sets the intended member count in the data-disk record and
// keeps every other field (DEVICE, UUID): the boot script assembles by UUID.
func withMembers(record string, n int) string {
	line := strings.TrimSpace(record)
	if membersField.MatchString(line) {
		return membersField.ReplaceAllString(line, fmt.Sprintf("MEMBERS=%d", n))
	}
	return strings.TrimSpace(fmt.Sprintf("%s MEMBERS=%d", line, n))
}
