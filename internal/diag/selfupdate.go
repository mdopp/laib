package diag

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SelfUpdate is what scripts/self-update.sh (#547) found at its last run —
// Laib's own update, before the nightly image run. It writes
// Home/self-update.json; diag and laib updates read it.
type SelfUpdate struct {
	At      time.Time `json:"at"`
	Running string    `json:"running"`
	Latest  string    `json:"latest"`
	Result  string    `json:"result"` // current | updated | off | skipped | failed
	Detail  string    `json:"detail"`
}

// ReadSelfUpdate reads the last run; false before the first one.
func ReadSelfUpdate(home string) (SelfUpdate, bool) {
	var r SelfUpdate
	b, err := os.ReadFile(filepath.Join(home, "self-update.json"))
	return r, err == nil && json.Unmarshal(b, &r) == nil
}

// selfUpdateFindings: yellow when Laib does not keep itself current — no
// self-update.sh in bin (a box installed before #547), a release rolled
// back, off while a newer one is out, or no look for more than eight days.
func selfUpdateFindings(home, bin string, now time.Time) []Finding {
	f := func(detail, fix string) []Finding {
		return []Finding{{Severity: "gelb", Probe: "self-update", Detail: detail, Fix: fix}}
	}
	if _, err := os.Stat(filepath.Join(bin, "self-update.sh")); err != nil {
		return f("Laib aktualisiert sich nicht selbst: self-update.sh fehlt in "+bin, "install-box.sh einmal von Hand (es legt self-update.sh an; danach kommt jedes Release nachts von selbst)")
	}
	r, ok := ReadSelfUpdate(home)
	switch {
	case !ok:
		return nil // the first night is still ahead
	case r.Result == "failed":
		return f("Laib "+r.Latest+" zurückgenommen, "+r.Running+" läuft: "+r.Detail, "self-update.log in LAIB_HOME und laib journal; ein neueres Release wird wieder versucht, dieses nicht")
	case r.Result == "off" && r.Latest != r.Running:
		return f("Laib läuft "+r.Running+", "+r.Latest+" ist da: die Selbst-Aktualisierung ist aus (box.yml updates.laib: off)", "box.yml updates.laib: auto, oder die Version, die bleiben soll")
	case now.Sub(r.At) > 8*24*time.Hour:
		return f(fmt.Sprintf("Laib hat seit %d Tagen nicht nach einem neuen Release gesehen", int(now.Sub(r.At).Hours()/24)), "systemctl --user status laib-images.timer; journalctl --user -u laib-images")
	}
	return nil
}
