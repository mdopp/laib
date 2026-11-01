package diag

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// `laib planned <service>` (#978): the box's one answer to „was this stop
// planned, and what does this service depend on“. The log review asked it
// in its own script with a hand-kept map (#975) while diag answered it for
// the look (#907); now both read it here.
//
// Planned is a unit's "Stopping" line (systemd writes one only when someone
// asked; a crash writes "Main process exited", its automatic restart no
// "Stopping") that a journal entry accounts for: an apply, the nightly
// backup, an updates run, Laib's own restart (ready), converge, restart,
// stop, start, repair, rollback, restore, pause or resume within three
// minutes of its span. Its window runs from two minutes before the stop to
// three after the unit's next "Started". Right now, an apply that holds the
// service or a pause (#907) is planned too, before its entry is written.

const planBefore, planAfter, planPad = 2 * time.Minute, 3 * time.Minute, 3 * time.Minute

// plannedVerbs: the journal's verbs that stop a unit on purpose, with the
// word the notes use.
var plannedVerbs = map[string]string{"apply": "Anwenden", "backup": "Sicherung", "updates": "Update", "ready": "Laib-Neustart",
	"converge": "converge", "restart": "laib restart", "stop": "laib stop", "start": "laib start", "repair": "Reparatur",
	"rollback": "Rücknahme", "restore": "Wiederherstellung", "pause": "laib pause", "resume": "laib resume"}

var stopLine = regexp.MustCompile(`^(\S+) \S+ systemd\[\d+\]: (Stopping|Started) (\S+)`)

// OwnUnits are Laib's own user units, read under short names like a
// service's (#947); laib is also what a needs: [mcp] service depends on.
var OwnUnits = map[string][]string{
	"laib": {"laib.service"}, "operator": {"laib-operator.service"}, "notify": {"laib-notify.service", "laib-notify.timer"},
	"updates": {"laib-images.service", "laib-images.timer"}, "haushalt": {"laib-haushalt.service"}, "backup": {"laib-backup.service", "laib-backup-check.service"},
}

// Window is one planned stop of a service: its units' first Stopping and
// last Started that one journal entry accounts for.
type Window struct {
	Service string    `json:"service"`
	Verb    string    `json:"verb"`
	Label   string    `json:"label"`
	Stop    time.Time `json:"stop"`
	Start   time.Time `json:"start"`
	From    time.Time `json:"from"` // a line of it or of a service depending on it is expected from here …
	To      time.Time `json:"to"`   // … to here
}

func (w Window) say() string {
	return w.Label + " von " + w.Service + " um " + w.Stop.Local().Format("15:04")
}

// Planned is what `laib planned` answers.
type Planned struct {
	Service  string    `json:"service"`
	Depends  []string  `json:"depends"`          // what its lines may follow from, transitively
	Now      string    `json:"now,omitempty"`    // why a red is expected right now, "" when nothing is planned now
	Windows  []Window  `json:"windows"`          // its and its dependencies' planned stops since --since, oldest first
	Findings []Finding `json:"findings"`         // its units', health's and status file's findings now; [] when none
	Unread   []string  `json:"unread,omitempty"` // "journal" or a service whose units could not be read: not checked
}

// Depends: the services whose planned stop explains a service's errors —
// its after:, its uses:, laib for needs: [mcp], the SSO role for an oidc:
// client — and theirs, sorted, without itself.
func Depends(st *state.State, name string) []string {
	seen, todo := map[string]bool{name: true}, []string{name}
	for len(todo) > 0 {
		svc := st.Services[todo[0]]
		todo = todo[1:]
		if svc == nil {
			continue
		}
		c := svc.Contract
		next := slices.Concat(c.After, c.Uses)
		if slices.Contains(c.Needs, "mcp") {
			next = append(next, "laib")
		}
		if c.OIDC != nil && st.Box.Roles.SSO != "" {
			next = append(next, st.Box.Roles.SSO)
		}
		for _, d := range next {
			if !seen[d] {
				seen[d] = true
				todo = append(todo, d)
			}
		}
	}
	delete(seen, name)
	return slices.Sorted(func(yield func(string) bool) {
		for d := range seen {
			if !yield(d) {
				return
			}
		}
	})
}

func journalTime(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05-0700"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// Windows reads the planned stops of a service and of what it depends on
// since a time, oldest first; the second result names what could not be
// read, and so was not checked.
func Windows(ctx context.Context, env *cli.Env, st *state.State, name string, since time.Time) ([]Window, []string) {
	var unread []string
	entries, err := journal.Read(env.Paths.Journal, since.Add(-time.Hour))
	if err != nil {
		unread = append(unread, "journal")
	}
	type key struct {
		svc string
		at  time.Time
	}
	byEntry := map[key]*Window{}
	var out []*Window
	for _, d := range append([]string{name}, Depends(st, name)...) {
		units := OwnUnits[d]
		if svc := st.Services[d]; svc != nil {
			units = svc.UnitNames()
		}
		for _, u := range units {
			text, err := sysd.Run(ctx, "journalctl", "--user", "-u", u, "--since", since.Local().Format("2006-01-02 15:04:05"), "--no-pager", "-o", "short-iso")
			if err != nil && text == "" {
				unread = append(unread, d)
				continue
			}
			for _, h := range halts(d, text, entries) { // the units of one service one entry stopped are one window
				w := byEntry[key{d, h.entry.T}]
				if w == nil {
					w = &Window{Service: d, Verb: h.entry.Verb, Label: plannedVerbs[h.entry.Verb], Stop: h.stop, Start: h.start}
					byEntry[key{d, h.entry.T}], out = w, append(out, w)
				}
				w.Stop, w.Start = minTime(w.Stop, h.stop), maxTime(w.Start, h.start)
			}
		}
	}
	ws := make([]Window, 0, len(out))
	for _, w := range out {
		w.From, w.To = w.Stop.Add(-planBefore), w.Start.Add(planAfter)
		ws = append(ws, *w)
	}
	sort.SliceStable(ws, func(i, j int) bool { return ws[i].Stop.Before(ws[j].Stop) })
	return ws, slices.Compact(unread)
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

type halt struct {
	entry       journal.Entry
	stop, start time.Time
}

// halts: a unit's stops the journal accounts for, each to the unit's next
// start; the entry nearest the stop names it (the backup, then the restart
// after it). Its own entries or the whole box's: an entry naming another
// service did not stop this one.
func halts(svc, text string, entries []journal.Entry) []halt {
	type line struct {
		t          time.Time
		verb, unit string
	}
	var ls []line
	for _, l := range strings.Split(text, "\n") {
		if m := stopLine.FindStringSubmatch(l); m != nil {
			if t, ok := journalTime(m[1]); ok {
				ls = append(ls, line{t, m[2], m[3]})
			}
		}
	}
	var out []halt
	for i, l := range ls {
		if l.verb != "Stopping" {
			continue
		}
		why := -1
		for k, e := range entries {
			from, to := e.T.Add(-time.Duration(e.MS)*time.Millisecond-planPad), e.T.Add(planPad)
			if _, ok := plannedVerbs[e.Verb]; !ok || (e.Service != "" && e.Service != svc) || l.t.Before(from) || l.t.After(to) {
				continue
			}
			if why < 0 || e.T.Sub(l.t).Abs() < entries[why].T.Sub(l.t).Abs() {
				why = k
			}
		}
		if why < 0 {
			continue
		}
		h := halt{entries[why], l.t, l.t}
		if j := slices.IndexFunc(ls[i+1:], func(n line) bool { return n.verb == "Started" && n.unit == l.unit }); j >= 0 {
			h.start = ls[i+1+j].t
		}
		out = append(out, h)
	}
	return out
}

// Holding is the window that holds t — the one under way first, then the
// nearest — or nil.
func Holding(ws []Window, t time.Time) *Window {
	var got *Window
	for i, w := range ws {
		if t.Before(w.From) || t.After(w.To) {
			continue
		}
		on, gotOn := !w.Stop.After(t), got != nil && !got.Stop.After(t)
		if got == nil || on && !gotOn || on == gotOn && w.Stop.Sub(t).Abs() < got.Stop.Sub(t).Abs() {
			got = &ws[i]
		}
	}
	return got
}

// PlannedCmd is `laib planned <service> [--since 24h]`.
func PlannedCmd(ctx context.Context, env *cli.Env, args []string) int {
	if len(args) == 0 {
		return env.Fail(errors.New("usage: laib planned <service> [--since 24h]"))
	}
	d := 24 * time.Hour
	if len(args) > 2 && args[1] == "--since" {
		var err error
		if d, err = time.ParseDuration(args[2]); err != nil || d <= 0 {
			return env.Fail(fmt.Errorf("--since %q: eine Dauer, z. B. 24h", args[2]))
		}
	}
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	if st.Services[args[0]] == nil {
		return env.Fail(fmt.Errorf("Dienst %q gibt es nicht", args[0]))
	}
	now := time.Now()
	p := Planned{Service: args[0], Depends: Depends(st, args[0]), Findings: Service(ctx, env, st, args[0])}
	p.Windows, p.Unread = Windows(ctx, env, st, p.Service, now.Add(-d))
	if p.Now = held(env, st, p.Service); p.Now == "" {
		if w := Holding(p.Windows, now); w != nil {
			p.Now = w.say()
		}
	}
	if p.Depends == nil {
		p.Depends = []string{}
	}
	if p.Findings == nil {
		p.Findings = []Finding{}
	}
	if env.JSON {
		return cli.Emit(env, p)
	}
	fmt.Fprintf(env.W(), "%s hängt ab von: %s\njetzt: %s\n", p.Service, cmpOr(strings.Join(p.Depends, ", "), "nichts"), cmpOr(p.Now, "nichts geplant"))
	for _, w := range p.Windows {
		fmt.Fprintf(env.W(), "geplant: %s–%s %s\n", w.Stop.Local().Format("02.01. 15:04"), w.Start.Local().Format("15:04"), w.say())
	}
	for _, f := range p.Findings {
		fmt.Fprintf(env.W(), "Befund: %s %s %s\n", f.Severity, f.Probe, f.Detail)
	}
	if len(p.Unread) > 0 {
		fmt.Fprintf(env.W(), "nicht lesbar, nicht geprüft: %s\n", strings.Join(p.Unread, ", "))
	}
	return cli.ExitOK
}

func cmpOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
