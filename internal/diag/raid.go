package diag

import (
	"fmt"

	"github.com/mdopp/laib/internal/disk"
)

// raidFindings is the diag probe: red when a member that was there is gone
// (or the array is missing), yellow when the mirror was never added.
func raidFindings(mdstat string, expectedMembers int) []Finding {
	arrays := disk.ParseMdstat(mdstat)
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
