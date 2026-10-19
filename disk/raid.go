package disk

import (
	"context"
	"fmt"
	"os"
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
	if _, members := MarkerMembers(); members > 0 {
		if b, err := os.ReadFile(mdstatPath); err == nil {
			out = append(out, raidFindings(string(b), members)...)
		}
	}
	if hardware {
		out = append(out, hardwareFindings(ctx)...)
	}
	return out
}

// raidFindings: red when a member that was there is gone
// (or the array is missing), yellow when the mirror was never added.
func raidFindings(mdstat string, expectedMembers int) []Finding {
	arrays := ParseMdstat(mdstat)
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
