package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// The Operator's heartbeat. Silence is the failure mode nobody notices:
// an operator that is enabled but not running, or running and never
// finishing a tick, must show in status, diag and on the pages — not only
// in `laib operator status`, which nobody runs when all seems fine.
//
// Inputs: box.yml says enabled; the user unit is active; /ready on the
// operator's port answers; the newest journal note by ops (every tick ends
// with one) is younger than three ticks.

type opFacts struct {
	Enabled    bool
	ModelURL   string
	UnitActive bool
	Answers    bool
	LastNote   time.Time
	EnabledAt  time.Time // the last `operator enable` in the journal
	Tick       time.Duration
	Now        time.Time
}

// operatorState turns the facts into one word for the readiness line and
// at most one diag finding.
func operatorState(f opFacts) (string, *Finding) {
	switch {
	case !f.Enabled && f.ModelURL == "":
		return "aus", nil // a box without a model: nothing to expect
	case !f.Enabled:
		return "aus", &Finding{Severity: "gelb", Probe: "operator", Detail: "Operator nicht aktiviert: niemand repariert nachts, kein Morgenbericht", Fix: "laib operator enable"}
	case !f.UnitActive:
		return "fehlt", &Finding{Severity: "rot", Probe: "operator", Detail: "Operator ist aktiviert, aber laib-operator.service läuft nicht", Fix: "journalctl --user -u laib-operator; laib operator enable"}
	case !f.Answers:
		return "antwortet nicht", &Finding{Severity: "rot", Probe: "operator", Detail: "laib-operator.service läuft, aber der Operator antwortet nicht auf 127.0.0.1:5901", Fix: "journalctl --user -u laib-operator"}
	case f.LastNote.IsZero() && !f.EnabledAt.IsZero() && f.Now.Sub(f.EnabledAt) > 3*f.Tick:
		// enabled a while ago and never a note: the model does not answer or the loop is broken (#43)
		return "stumm", &Finding{Severity: "rot", Probe: "operator", Detail: fmt.Sprintf("Operator seit %s aktiviert und noch keine einzige Notiz (Takt %s): Modell nicht erreichbar oder die Schleife läuft nicht", f.Now.Sub(f.EnabledAt).Round(time.Minute), f.Tick), Fix: "journalctl --user -u laib-operator; laib operator status"}
	case f.LastNote.IsZero():
		return "startet", nil // no note yet: right after enable; the next tick decides
	case f.Now.Sub(f.LastNote) > 3*f.Tick:
		return "stumm", &Finding{Severity: "gelb", Probe: "operator", Detail: fmt.Sprintf("Operator läuft, aber die letzte Notiz ist %s alt (Takt %s): Modell nicht erreichbar oder Tick hängt", f.Now.Sub(f.LastNote).Round(time.Minute), f.Tick), Fix: "laib logs operator | journalctl --user -u laib-operator"}
	}
	return "läuft", nil
}

// operatorFacts gathers the inputs on the box.
func operatorFacts(ctx context.Context, env *cliEnv, st *state.State) opFacts {
	f := opFacts{Enabled: st.Box.Operator.Enabled, ModelURL: st.ModelURL(), Now: time.Now()}
	f.Tick, _ = time.ParseDuration(st.Box.Operator.Tick)
	if f.Tick == 0 {
		f.Tick = 10 * time.Minute
	}
	if !f.Enabled {
		return f
	}
	f.UnitActive = sysd.Active(ctx, "laib-operator.service") == "active"
	c := &http.Client{Timeout: 2 * time.Second}
	if resp, err := c.Get("http://127.0.0.1:5901/ready"); err == nil {
		resp.Body.Close()
		f.Answers = resp.StatusCode < 500
	}
	if es, err := journal.Read(env.paths.Journal, f.Now.Add(-24*time.Hour)); err == nil {
		for _, e := range es {
			if e.Actor == "ops" && e.Verb == "note" && e.T.After(f.LastNote) {
				f.LastNote = e.T
			}
			if e.Verb == "operator" && e.Detail == "enable" && e.T.After(f.EnabledAt) {
				f.EnabledAt = e.T
			}
		}
	}
	return f
}
