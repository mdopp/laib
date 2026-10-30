package diag

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
	"github.com/mdopp/laib/internal/token"
)

// The Operator's heartbeat. Silence is the failure mode nobody notices:
// an operator that is enabled but not running, or running and never
// finishing a tick, must show in status, diag and on the pages — not only
// in `laib operator status`, which nobody runs when all seems fine.
//
// Inputs: box.yml says enabled; the user unit is active; /ready on the
// operator's port answers; its stored bearer still opens MCP (#500); the last work /ready reports (a look
// that found nothing new writes no note, #543), or without it the newest journal note by ops, is younger
// than three ticks.

type OpFacts struct {
	Enabled    bool
	ModelURL   string
	UnitActive bool
	// the stored OPERATOR_TOKEN is no live ops token: every MCP call gets 401 (#500)
	Unauthorized bool
	Answers      bool
	LastNote     time.Time
	Looked       time.Time // /ready's last look without an error (#543)
	Work         OpWork    // /ready's work: what it does now and why it waits (#792)
	EnabledAt    time.Time // the last `operator enable` in the journal
	Tick         time.Duration
	Now          time.Time
}

// OpWork is the Operator's /ready work (operator.Work): the incident it
// works on, since when, its last step, why it waits, how many wait.
type OpWork struct {
	Incident string    `json:"incident,omitempty"`
	Since    time.Time `json:"since,omitzero"`
	Step     string    `json:"step,omitempty"`
	Waiting  string    `json:"waiting,omitempty"`
	Queued   int       `json:"queued,omitempty"`
	// each skill's last run (#839): when, how it ended and why, what it caused
	Skills map[string]struct {
		At      time.Time `json:"at"`
		Outcome string    `json:"outcome"`
		Why     string    `json:"why,omitempty"`
		Caused  string    `json:"caused,omitempty"`
	} `json:"skills,omitempty"`
	// what the model's sessions cost on Day (#859): tokens in and out, milliseconds
	Model *struct {
		Day                   string
		Sessions, In, Out, Ms int
	} `json:"model,omitempty"`
}

// OperatorState turns the facts into one word for the readiness line and
// at most one diag finding.
func OperatorState(f OpFacts) (string, *Finding) {
	// /ready's last work is the heartbeat; a note counts only without it:
	// a slot that waits writes one, and that is no work done (#858)
	if !f.Looked.IsZero() {
		f.LastNote = f.Looked
	}
	switch {
	case !f.Enabled && f.ModelURL == "":
		return "aus", nil // a box without a model: nothing to expect
	case !f.Enabled:
		return "aus", &Finding{Severity: "gelb", Probe: "operator", Detail: "Operator nicht aktiviert: niemand repariert nachts, kein Morgenbericht", Fix: "laib operator enable", Repair: []string{"operator", "enable"}}
	case !f.UnitActive:
		return "fehlt", &Finding{Severity: "rot", Probe: "operator", Detail: "Operator ist aktiviert, aber laib-operator.service läuft nicht", Fix: "laib logs operator; laib operator enable"}
	case f.Unauthorized:
		return "abgewiesen", &Finding{Severity: "rot", Probe: "operator", Detail: "Der Operator läuft mit einem widerrufenen oder fehlenden ops-Token: jeder Aufruf an laib endet mit 401", Fix: "laib operator enable (prägt ein neues Token)", Repair: []string{"operator", "enable"}}
	case !f.Answers:
		return "antwortet nicht", &Finding{Severity: "rot", Probe: "operator", Detail: "laib-operator.service läuft, aber der Operator antwortet nicht auf 127.0.0.1:5901", Fix: "laib logs operator"}
	case f.LastNote.IsZero() && !f.EnabledAt.IsZero() && f.Now.Sub(f.EnabledAt) > 3*f.Tick:
		// enabled a while ago and never a note: the model does not answer or the loop is broken (#43)
		return "stumm", &Finding{Severity: "rot", Probe: "operator", Detail: fmt.Sprintf("Operator seit %s aktiviert und noch keine einzige Notiz (Takt %s): Modell nicht erreichbar oder die Schleife läuft nicht", f.Now.Sub(f.EnabledAt).Round(time.Minute), f.Tick), Fix: "laib logs operator; laib operator status"}
	case f.LastNote.IsZero():
		return "startet", nil // no note yet: right after enable; the next tick decides
	case f.Now.Sub(f.LastNote) > 3*f.Tick && f.Work.Waiting != "":
		// a look that waits with its reason answers, it does not hang (#931): a
		// cold household model after llama restarted, a window, a person's cache
		return "wartet", &Finding{Severity: "info", Probe: "operator", Detail: fmt.Sprintf("Operator wartet seit %s mit seinem Blick: %s", f.Now.Sub(f.LastNote).Round(time.Minute), f.Work.Waiting), Wartet: f.Work.Waiting}
	case f.Now.Sub(f.LastNote) > 3*f.Tick:
		return "stumm", &Finding{Severity: "gelb", Probe: "operator", Detail: fmt.Sprintf("Operator läuft, aber sein letzter Blick ist %s alt (Takt %s): Modell nicht erreichbar oder Tick hängt", f.Now.Sub(f.LastNote).Round(time.Minute), f.Tick), Fix: "laib logs operator"}
	}
	return "läuft", nil
}

// OperatorFacts gathers the inputs on the box.
func OperatorFacts(ctx context.Context, env *cli.Env, st *state.State) OpFacts {
	f := OpFacts{Enabled: st.Box.Operator.Enabled, ModelURL: st.ModelURL(), Now: time.Now()}
	f.Tick, _ = time.ParseDuration(st.Box.Operator.Tick)
	if f.Tick == 0 {
		f.Tick = 10 * time.Minute
	}
	if !f.Enabled {
		return f
	}
	f.UnitActive = sysd.Active(ctx, "laib-operator.service") == "active"
	f.Unauthorized = !OperatorTokenValid(env)
	c := &http.Client{Timeout: 2 * time.Second}
	if resp, err := c.Get("http://127.0.0.1:5901/ready"); err == nil {
		var ready struct {
			Looked time.Time
			Work   OpWork
		}
		json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&ready)
		resp.Body.Close()
		f.Answers, f.Looked, f.Work = resp.StatusCode < 500, ready.Looked, ready.Work
	}
	if es, err := journal.Read(env.Paths.Journal, f.Now.Add(-24*time.Hour)); err == nil {
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

// OperatorTokenValid reports whether OPERATOR_TOKEN in secrets.age is a live
// ops token in the store — what the MCP handler checks on every call.
func OperatorTokenValid(env *cli.Env) bool {
	sec, err := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	if err != nil || sec[render.OperatorToken] == "" {
		return false
	}
	store, err := token.Load(cli.TokenPath(env))
	if err != nil {
		return false
	}
	rec, ok := store.Lookup(sec[render.OperatorToken], time.Now())
	return ok && rec.Hand == "ops"
}
