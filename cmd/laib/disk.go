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

// cmdDisk: status | add <device> (humans only: sudo, irreversible for the
// device's content). add partitions the whole device, joins it to
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
			fmt.Printf("%s %s %d/%d [%s] %s %s\n", a.Name, a.Level, a.Active, a.Slots, a.Status, strings.Join(a.Members, ","), a.Resync)
		}
		if members == 0 {
			fmt.Println("keine Datenplatte: /mnt/data liegt auf der OS-Platte")
		}
		return 0
	}
	if args[0] != "add" || len(args) != 2 {
		return fail(errors.New("usage: laib disk status | add <device, e.g. /dev/sdb>"))
	}
	if !strings.HasPrefix(env.actor, "human") {
		return fail(errors.New("disk add: a human runs this (it wipes the device)"))
	}
	dev := args[1]
	dataDev, members := markerMembers()
	if dataDev == "" {
		return fail(errors.New("no data RAID on this box (/etc/laib/data-disk missing)"))
	}
	if members >= 2 {
		return fail(errors.New("the array already has two members"))
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
	if out, err := sysd.Run(ctx, "sudo", "mdadm", "--manage", "/dev/md/data", "--add", part); err != nil {
		return fail(fmt.Errorf("mdadm --add: %v: %s", err, out))
	}
	if out, err := sysd.Run(ctx, "sudo", "sh", "-c", fmt.Sprintf("echo 'DEVICE=%s MEMBERS=2' > %s", dataDev, dataDiskMarker)); err != nil {
		return fail(fmt.Errorf("marker: %v: %s", err, out))
	}
	fmt.Printf("%s joined /dev/md/data; it syncs now — laib disk status shows the progress\n", part)
	return 0
}
