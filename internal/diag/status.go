package diag

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"time"

	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// Report is a service's status file as laib read it (#309).
type Report struct {
	Detail   string
	At       time.Time // the file's "at", else its modification time
	Findings []Finding
	Found    bool
	// Eigenes are the file's further fields, by name, as written (#688);
	// Gekappt names those left out for EigenesMax.
	Eigenes map[string]json.RawMessage
	Gekappt []string
}

// EigenesMax caps a status file's own fields in one answer, in bytes.
const EigenesMax = 32 << 10

// ReadStatus reads the service's status file fresh: a file read on every
// status, /ready and diag, no cache and no watcher. A missing or unreadable
// file is no report; the template's own unit state still shows.
func ReadStatus(st *state.State, svc *state.Service) Report {
	p := state.StatusPath(st, svc)
	if p == "" {
		return Report{}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return Report{}
	}
	var f struct {
		At       string `json:"at"`
		Detail   string `json:"detail"`
		Findings []struct {
			Severity, Detail, Fix, Topic, Effect, Undo string
			Repair                                     []string
			Unmeasured                                 bool
		} `json:"findings"`
	}
	var all map[string]json.RawMessage
	if json.Unmarshal(b, &f) != nil || json.Unmarshal(b, &all) != nil {
		return Report{}
	}
	r := Report{Detail: f.Detail, Found: true}
	n := 0
	for _, k := range slices.Sorted(maps.Keys(all)) {
		switch {
		case k == "at" || k == "detail" || k == "findings":
		case n+len(k)+len(all[k]) > EigenesMax:
			r.Gekappt = append(r.Gekappt, k)
		default:
			if r.Eigenes == nil {
				r.Eigenes = map[string]json.RawMessage{}
			}
			r.Eigenes[k], n = all[k], n+len(k)+len(all[k])
		}
	}
	if t, err := time.Parse(time.RFC3339, f.At); err == nil {
		r.At = t
	} else if fi, err := os.Stat(p); err == nil {
		r.At = fi.ModTime()
	}
	for _, x := range f.Findings {
		sev := x.Severity
		if sev != "rot" && sev != "gelb" {
			sev = "info"
		}
		r.Findings = append(r.Findings, Finding{Severity: sev, Probe: "status", Service: svc.Name, Detail: x.Detail, Fix: x.Fix,
			Topic: x.Topic, Repair: x.Repair, Unmeasured: x.Unmeasured, Effect: x.Effect, Undo: x.Undo}) // #759: a repair laib offers runs only as one (inbox.Repairable)
	}
	return r
}

// Age is the report's age in the words the pages use: "geprüft vor 12 min".
func Age(at, now time.Time) string {
	if at.IsZero() {
		return ""
	}
	switch d := now.Sub(at); {
	case d < time.Minute:
		return "geprüft gerade eben"
	case d < time.Hour:
		return fmt.Sprintf("geprüft vor %d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("geprüft vor %d h", int(d.Hours()))
	default:
		return fmt.Sprintf("geprüft vor %d Tagen", int(d.Hours()/24))
	}
}

// Stale: the service has a check and its report is missing or older than
// its stale duration.
func Stale(svc *state.Service, r Report, now time.Time) bool {
	s := svc.Contract.Status
	if s == nil || s.Check == "" {
		return false
	}
	d, _ := time.ParseDuration(cmp.Or(s.Stale, "5m"))
	return !r.Found || now.Sub(r.At) > d
}

// Refresh starts the service's check without waiting for it (systemctl
// --no-block): the caller shows the last report, the next look the new one.
// A check already running is left alone. It returns whether one runs now.
func Refresh(ctx context.Context, svc *state.Service) bool {
	s := svc.Contract.Status
	if s == nil || s.Check == "" {
		return false
	}
	switch sysd.Active(ctx, s.Check) {
	case "activating", "reloading":
		return true
	}
	_, err := sysd.Run(ctx, "systemctl", "--user", "start", "--no-block", s.Check)
	return err == nil
}
