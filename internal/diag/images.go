package diag

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/sysd"
)

// Behind (#528): since when `laib updates` has seen an image behind its
// channel — a floating tag's or a pin's newer digest, a pinned version's
// patch release — keyed "<service> <KEY>". laib updates writes
// Home/updates.json, diag reads it.
type Behind struct {
	Want  string    `json:"want"`
	Since time.Time `json:"since"`
}

func readBehind(home string) map[string]Behind {
	out := map[string]Behind{}
	b, _ := os.ReadFile(filepath.Join(home, "updates.json"))
	json.Unmarshal(b, &out)
	return out
}

// SeeBehind records what is behind now; a key that stays behind keeps its
// since, even when its channel moved again.
func SeeBehind(home string, now time.Time, want map[string]string) {
	old, out := readBehind(home), map[string]Behind{}
	for k, w := range want {
		out[k] = Behind{Want: w, Since: now}
		if o, ok := old[k]; ok {
			out[k] = Behind{Want: w, Since: o.Since}
		}
	}
	b, _ := json.Marshal(out)
	os.WriteFile(filepath.Join(home, "updates.json"), b, 0o644)
}

// imageFindings: yellow for an image more than 30 days behind, and for a box
// where nobody keeps the images current — laib-images.timer not running —
// or where podman's own timer runs beside it.
func imageFindings(ctx context.Context, home, only string, now time.Time) (out []Finding) {
	behind := readBehind(home)
	for _, k := range slices.Sorted(maps.Keys(behind)) {
		svc, key, _ := strings.Cut(k, " ")
		if d := now.Sub(behind[k].Since); d > 30*24*time.Hour && (only == "" || only == svc) {
			out = append(out, Finding{Severity: "gelb", Probe: "updates", Service: svc, Detail: fmt.Sprintf("%s liegt seit %d Tagen hinter seinem Kanal (%s)", key, int(d.Hours()/24), behind[k].Want),
				Fix: "laib updates; ein Dienst mit Daten wartet auf den Vorschlag im Eingang, einer ohne auf die Nacht (laib journal --verb updates zeigt eine Rücknahme)"})
		}
	}
	if only != "" {
		return out
	}
	if sysd.Active(ctx, "laib-images.timer") != "active" {
		out = append(out, Finding{Severity: "gelb", Probe: "updates", Detail: "laib-images.timer läuft nicht: niemand hält die Images aktuell (er ersetzt podman-auto-update.timer, #528)", Fix: "laib apply (rendert und startet laib-images.timer)"})
	}
	if sysd.Active(ctx, "podman-auto-update.timer") == "active" {
		out = append(out, Finding{Severity: "gelb", Probe: "updates", Detail: "podman-auto-update.timer läuft neben laib-images.timer: ein zweiter Update-Weg ohne Laibs Sperre, Prüfung und Rücknahme", Fix: "systemctl --user mask --now podman-auto-update.timer (install-box.sh --box-config)"})
	}
	return out
}
