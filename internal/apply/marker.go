package apply

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/state"
)

// A maintenance marker (#978, the owner: „im Vorfeld einen Marker setzen,
// dass jetzt bis x Uhr ein Neustart durchgeführt wird“): every action that
// stops or starts services on purpose — apply and converge, laib restart and
// start, the backup's quiesce, the updates run's restarts, Laib's own start
// — says so before it acts: the services it acts on, the ones depending on
// them, who, why, and until when it will be done. While it holds, their
// unit and health reds are expected (diag) and their error lines are no
// pattern (the log review); the action clears it when it ends. A marker
// still there after its until is itself red: the restart did not finish
// cleanly, and only then is there something to analyse. One file per
// marker in the home (not the state repo: no commit), its start and end in
// the journal; an ended one is kept two days for the log review.

const (
	markerGrace = 3 * time.Minute // a dependent's lines after the action ended: it reconnects
	markerKeep  = 48 * time.Hour
)

// Marker is one announced stop.
type Marker struct {
	ID         string    `json:"id"`
	Verb       string    `json:"verb"`
	By         string    `json:"by"`
	Reason     string    `json:"reason,omitempty"`
	Services   []string  `json:"services"`   // what the action acts on
	Dependents []string  `json:"dependents"` // what depends on them (state.Depends)
	Since      time.Time `json:"since"`
	Until      time.Time `json:"until"`
	End        time.Time `json:"end,omitzero"`     // when the action cleared it
	Result     string    `json:"result,omitempty"` // green | red | nachgeholt, at its end
}

// Words is the action in the notes' words.
var Words = map[string]string{"apply": "Anwenden", "converge": "converge", "backup": "Sicherung", "updates": "Update",
	"restart": "laib restart", "start": "laib start", "ready": "Laib-Neustart"}

func markerDir(paths boxfs.Paths) string { return filepath.Join(paths.Home, "markers") }

// Say is the marker in one German phrase: „Anwenden von home-assistant bis 10:13“.
func (m Marker) Say() string {
	what := strings.Join(m.Services, ", ")
	if len(m.Services) > 3 {
		what = fmt.Sprintf("%d Diensten", len(m.Services))
	}
	return fmt.Sprintf("%s von %s bis %s", Words[m.Verb], what, m.Until.Local().Format("15:04"))
}

// Window is from when to when a service's lines and reds are expected: from
// the marker on to its end (a dependent three minutes longer), or its until
// while it holds.
func (m Marker) Window(svc string) (from, to time.Time, ok bool) {
	dep := slices.Contains(m.Dependents, svc)
	if !dep && !slices.Contains(m.Services, svc) {
		return from, to, false
	}
	to = m.Until
	if !m.End.IsZero() {
		to = m.End
	}
	if dep {
		to = to.Add(markerGrace)
	}
	return m.Since, to, true
}

// Holds: t falls in the marker's window for svc.
func (m Marker) Holds(svc string, t time.Time) bool {
	from, to, ok := m.Window(svc)
	return ok && !t.Before(from) && !t.After(to)
}

// Overdue: not cleared by its until.
func (m Marker) Overdue(now time.Time) bool { return m.End.IsZero() && now.After(m.Until) }

// Mark sets a marker before an action acts on services, journals it and
// returns the function that clears it with the action's result. Nothing to
// act on marks nothing.
func Mark(st *state.State, paths boxfs.Paths, verb, by, reason string, services []string, d time.Duration) func(green bool) {
	services = slices.DeleteFunc(slices.Clone(services), func(s string) bool { return st.Services[s] == nil && s != "laib" })
	if len(services) == 0 {
		return func(bool) {}
	}
	now := time.Now().UTC()
	m := Marker{ID: fmt.Sprintf("%s-%d", verb, now.UnixNano()), Verb: verb, By: by, Reason: reason, Services: services,
		Dependents: st.Dependents(services...), Since: now, Until: now.Add(d)}
	writeMarker(paths, m)
	journal.Append(paths.Journal, journal.Entry{T: now, Actor: by, Verb: "marker", Service: strings.Join(services, ","), Reason: verb, Result: "info",
		Detail: "geplant: " + m.Say() + strings.TrimSuffix(" (betrifft auch "+strings.Join(m.Dependents, ", ")+")", " (betrifft auch )")})
	return func(green bool) {
		m.End, m.Result = time.Now().UTC(), map[bool]string{true: "green", false: "red"}[green]
		writeMarker(paths, m)
		journal.Append(paths.Journal, journal.Entry{Actor: by, Verb: "marker", Service: strings.Join(services, ","), Reason: verb, Result: "info",
			Detail: fmt.Sprintf("fertig (%s): %s", map[bool]string{true: "grün", false: "rot"}[green], m.Say())})
		if !green {
			return
		}
		for _, o := range Markers(paths) { // an overdue one this action did again is done (#978)
			if o.Overdue(m.End) && !slices.ContainsFunc(o.Services, func(s string) bool { return !slices.Contains(services, s) }) {
				o.End, o.Result = m.End, "nachgeholt"
				writeMarker(paths, o)
			}
		}
	}
}

// Window records a stop already over, or one that ends by itself at a known
// time (Laib's own start: its dependents reconnect), as an ended marker.
func Window(st *state.State, paths boxfs.Paths, verb, by string, services []string, d time.Duration) {
	now := time.Now().UTC()
	writeMarker(paths, Marker{ID: fmt.Sprintf("%s-%d", verb, now.UnixNano()), Verb: verb, By: by, Services: services,
		Dependents: st.Dependents(services...), Since: now, Until: now.Add(d), End: now.Add(d), Result: "green"})
}

func writeMarker(paths boxfs.Paths, m Marker) {
	os.MkdirAll(markerDir(paths), 0o755)
	b, _ := json.Marshal(m)
	boxfs.WriteAtomic(filepath.Join(markerDir(paths), m.ID+".tmp"), filepath.Join(markerDir(paths), m.ID+".json"), b, 0o644)
}

// Markers are the markers kept, oldest first: those that hold, those
// overdue and those ended in the last two days; older ones are removed on
// the way.
func Markers(paths boxfs.Paths) []Marker {
	files, _ := filepath.Glob(filepath.Join(markerDir(paths), "*.json"))
	var out []Marker
	for _, f := range files {
		var m Marker
		if b, err := os.ReadFile(f); err != nil || json.Unmarshal(b, &m) != nil || m.ID == "" {
			continue
		}
		if !m.End.IsZero() && time.Since(m.End) > markerKeep || m.End.IsZero() && time.Since(m.Until) > 24*time.Hour {
			os.Remove(f) // an overdue one is red a day, then laib lets it go
			continue
		}
		out = append(out, m)
	}
	slices.SortFunc(out, func(a, b Marker) int { return a.Since.Compare(b.Since) })
	return out
}
