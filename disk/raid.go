package disk

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
)

// Finding is one result of `laib-disk probe`, in diag's JSON shape (#443).
type Finding struct {
	Severity string `json:"severity"`
	Probe    string `json:"probe"`
	Service  string `json:"service,omitempty"`
	Detail   string `json:"detail"`
	Fix      string `json:"fix,omitempty"`
}

// Probe is what diag shows of the disks: the data RAID always, SMART and the
// temperature when hardware is asked for (diag of the whole box).
func Probe(ctx context.Context, hardware bool) []Finding {
	out := []Finding{}
	if dev, members := DataArray(); dev != "" {
		if b, err := os.ReadFile(mdstatPath); err == nil {
			out = append(out, raidFindings(string(b), mdName(dev), members)...)
		}
	}
	if hardware {
		out = append(out, hardwareFindings(ctx)...)
	}
	return out
}

// raidFindings about the data array (data, its mdstat name): red when a
// member is gone or failed (or the array is missing), yellow while it runs
// without a mirror. expectedMembers is Laib's record, 0 for an array Laib
// did not create (#488): then a missing mirror may never have been added,
// mdadm --detail tells.
func raidFindings(mdstat, data string, expectedMembers int) []Finding {
	if data == "" {
		return nil // no data disk on this box: nothing to say
	}
	arrays := ParseMdstat(mdstat)
	i := slices.IndexFunc(arrays, func(a Array) bool { return a.Name == data })
	if i < 0 {
		return []Finding{{Severity: "rot", Probe: "raid", Detail: "/mnt/data: kein RAID aktiv (" + data + "), aber die Box hat eine Datenplatte", Fix: "cat /proc/mdstat; journalctl -u laib-data-disk"}}
	}
	a := arrays[i]
	lost := "ein Plattenausfall verliert /mnt/data"
	switch {
	case len(a.Failed) > 0:
		return []Finding{{Severity: "rot", Probe: "raid", Detail: fmt.Sprintf("%s: %s ausgefallen, %d von %d Platten: %s", a.Name, strings.Join(a.Failed, ", "), a.Active, a.Slots, lost), Fix: "mdadm --detail /dev/" + a.Name + "; Platte tauschen, dann laib disk add /dev/sdX"}}
	case a.Resync != "":
		return []Finding{{Severity: "gelb", Probe: "raid", Detail: fmt.Sprintf("%s: Spiegel wird aufgebaut (%s), bis dahin %s", a.Name, a.Resync, lost), Fix: "laib disk status zeigt den Fortschritt"}}
	case a.Active >= a.Slots:
		return nil
	case expectedMembers == 0:
		return []Finding{{Severity: "gelb", Probe: "raid", Detail: fmt.Sprintf("%s: %d von %d Platten, Spiegel fehlt: %s", a.Name, a.Active, a.Slots, lost), Fix: "mdadm --detail /dev/" + a.Name + " (Failed Devices 0 und die Creation Time der Einrichtung: nie gespiegelt), dann laib disk add /dev/sdX"}}
	case a.Active >= expectedMembers:
		return []Finding{{Severity: "gelb", Probe: "raid", Detail: fmt.Sprintf("%s: %d von %d Platten, kein Spiegel — gewollt, bis eine zweite Platte kommt", a.Name, a.Active, a.Slots), Fix: "laib disk add /dev/sdX"}}
	}
	return []Finding{{Severity: "rot", Probe: "raid", Detail: fmt.Sprintf("%s: nur %d von %d Platten, eine ist ausgefallen (war %d)", a.Name, a.Active, a.Slots, expectedMembers), Fix: "cat /proc/mdstat; Platte tauschen, dann laib disk add"}}
}
