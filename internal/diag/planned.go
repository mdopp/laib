package diag

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/state"
)

// `laib planned <service>` (#978): the box's one answer to „is this stop
// planned, and what does this service depend on“, for diag's look and the
// skills alike. Planned is what an action announced before it acted: its
// maintenance marker (apply.Mark), or a hand's pause (#441). diag holds a
// red by it; the log review drops the lines in a marker's window.

// Planned is what `laib planned` answers.
type Planned struct {
	Service  string    `json:"service"`
	Depends  []string  `json:"depends"`       // what its lines may follow from, transitively
	Now      string    `json:"now,omitempty"` // why a red is expected right now, "" when nothing is planned now
	Windows  []Window  `json:"windows"`       // the markers that hold or held it, oldest first
	Findings []Finding `json:"findings"`      // its units', health's and status file's findings now; [] when none
}

// Window is a marker as one service sees it.
type Window struct {
	apply.Marker
	Label   string    `json:"label"`   // the action in the notes' words: Anwenden
	From    time.Time `json:"from"`    // a line of the service is expected from here …
	To      time.Time `json:"to"`      // … to here
	Overdue bool      `json:"overdue"` // not cleared by its until: the restart did not finish cleanly
}

// Windows are the markers that hold or held a service, oldest first.
func Windows(env *cli.Env, name string) []Window {
	var out []Window
	for _, m := range apply.Markers(env.Paths) {
		if from, to, ok := m.Window(name); ok {
			out = append(out, Window{m, apply.Words[m.Verb], from, to, m.Overdue(time.Now())})
		}
	}
	return out
}

// held is why a service's red is expected now (#907, #978): a marker holds
// it or a hand paused it or what it depends on (laib stop, the lease).
func held(env *cli.Env, st *state.State, name string) string {
	now := time.Now()
	for _, m := range apply.Markers(env.Paths) {
		if m.Holds(name, now) && !m.Overdue(now) {
			return "geplant: " + m.Say()
		}
	}
	paused := apply.Paused(env.Paths)
	for _, d := range append([]string{name}, st.Depends(name)...) {
		if p, ok := paused[d]; ok {
			return strings.TrimPrefix(d+" ", name+" ") + "pausiert von " + p.By + " bis " + p.Until.Local().Format("02.01. 15:04")
		}
	}
	return ""
}

// overdue: a marker not cleared by its until is red for each service it
// acted on (#978) — the restart did not finish cleanly; only now is there
// something to analyse.
func overdue(env *cli.Env, only string) []Finding {
	var out []Finding
	for _, m := range apply.Markers(env.Paths) {
		for _, s := range m.Services {
			if m.Overdue(time.Now()) && (only == "" || only == s) {
				out = append(out, Finding{Severity: "rot", Probe: "wartung", Service: s, Fix: "laib logs " + s,
					Detail: fmt.Sprintf("Der Neustart von %s ist bis %s nicht sauber fertig geworden (%s von %s seit %s)", s, m.Until.Local().Format("15:04"), apply.Words[m.Verb], m.By, m.Since.Local().Format("15:04")),
					Say:    fmt.Sprintf("%s ist nach dem geplanten Neustart nicht rechtzeitig wieder da.", s)})
			}
		}
	}
	return out
}

// PlannedCmd is `laib planned <service>`.
func PlannedCmd(ctx context.Context, env *cli.Env, args []string) int {
	if len(args) == 0 {
		return env.Fail(errors.New("usage: laib planned <service>"))
	}
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	if st.Services[args[0]] == nil {
		return env.Fail(fmt.Errorf("Dienst %q gibt es nicht", args[0]))
	}
	p := Planned{Service: args[0], Depends: append([]string{}, st.Depends(args[0])...), Now: held(env, st, args[0]),
		Windows: append([]Window{}, Windows(env, args[0])...), Findings: append([]Finding{}, Service(ctx, env, st, args[0])...)}
	p.Findings = append(p.Findings, overdue(env, args[0])...)
	if env.JSON {
		return cli.Emit(env, p)
	}
	fmt.Fprintf(env.W(), "%s hängt ab von: %s\njetzt: %s\n", p.Service, cmpOr(strings.Join(p.Depends, ", "), "nichts"), cmpOr(p.Now, "nichts geplant"))
	for _, w := range p.Windows {
		fmt.Fprintf(env.W(), "geplant %s–%s: %s%s\n", w.From.Local().Format("02.01. 15:04"), w.To.Local().Format("15:04"), w.Say(), map[bool]string{true: " — nicht rechtzeitig fertig"}[w.Overdue])
	}
	for _, f := range p.Findings {
		fmt.Fprintf(env.W(), "Befund: %s %s %s\n", f.Severity, f.Probe, f.Detail)
	}
	return cli.ExitOK
}

func cmpOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
