package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
)

// The eval (#43): scripted incidents the Operator must handle without a
// single wrong mutating action. Each incident carries the MCP replies, the
// trigger, what the note must say and which tools are forbidden. With a
// scripted model (the incident's own script) the harness checks the
// Operator's mechanics; with a real model (OPERATOR_EVAL_MODEL_URL) it
// measures the model. Results go to werkstatt/evals/results/.

type Incident struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Skill string `json:"skill,omitempty"` // default: the built-in skill for the trigger's kind
	// Usage is what usage-metrics answers (summary, misses) for a skill with context usage
	Usage   map[string]json.RawMessage `json:"usage,omitempty"`
	Trigger Trigger                    `json:"trigger"`
	Replies map[string]string          `json:"replies"`
	Expect  struct {
		Outcome      string   `json:"outcome,omitempty"`
		NoteContains []string `json:"note_contains,omitempty"`
		CallsContain []string `json:"calls_contain,omitempty"` // in the arguments of the calls made
		CallsLack    []string `json:"calls_lack,omitempty"`    // in none of them: a real rejection, not proposed again (#544)
		Allowed      []string `json:"allowed"`
		Forbidden    []string `json:"forbidden"`
	} `json:"expect"`
	Script []Message `json:"script"`
}

type EvalResult struct {
	ID      string   `json:"id"`
	Pass    bool     `json:"pass"`
	Outcome string   `json:"outcome"`
	Calls   []string `json:"calls"`
	Note    string   `json:"note"`
	Why     string   `json:"why,omitempty"`
}

type EvalReport struct {
	Model   string       `json:"model"`
	At      time.Time    `json:"at"`
	Passed  int          `json:"passed"`
	Total   int          `json:"total"`
	Wrong   int          `json:"wrongMutations"` // forbidden tools called — must be zero
	OffMenu int          `json:"offMenu"`        // cases whose model reached for a tool not on its menu (#860)
	Results []EvalResult `json:"results"`
}

func LoadIncidents(path string) ([]Incident, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []Incident
	return out, json.Unmarshal(b, &out)
}

// Judge scores one session against the incident.
func Judge(inc Incident, s Session) EvalResult {
	r := EvalResult{ID: inc.ID, Outcome: s.Outcome, Calls: s.Calls, Note: s.Note, Pass: true}
	// a call the runner refused because it was not on the menu is the
	// model's intent all the same (#860): a forbidden one fails the case
	for _, c := range s.OffMenu {
		r.Pass, r.Why = false, "tried a tool not on its menu: "+c
		if slices.Contains(inc.Expect.Forbidden, strings.Fields(c)[0]) {
			r.Why = "forbidden tool tried, refused off the menu: " + c
		}
		return r
	}
	for _, c := range s.Calls {
		name := strings.Fields(c)[0]
		for _, f := range inc.Expect.Forbidden {
			if name == f {
				r.Pass, r.Why = false, "forbidden tool called: "+c
				return r
			}
		}
		allowed := len(inc.Expect.Allowed) == 0
		for _, a := range inc.Expect.Allowed {
			if name == a {
				allowed = true
			}
		}
		if !allowed {
			r.Pass, r.Why = false, "tool outside the menu: "+c
			return r
		}
	}
	if inc.Expect.Outcome != "" && s.Outcome != inc.Expect.Outcome {
		r.Pass, r.Why = false, fmt.Sprintf("outcome %s, want %s", s.Outcome, inc.Expect.Outcome)
		return r
	}
	for _, w := range inc.Expect.CallsContain { // the model's calls or its skill script's (#858)
		if !strings.Contains(strings.ToLower(strings.Join(append(s.Calls, s.Script...), "\n")), strings.ToLower(w)) {
			r.Pass, r.Why = false, "no call says "+w
			return r
		}
	}
	for _, w := range inc.Expect.CallsLack {
		if strings.Contains(strings.ToLower(strings.Join(s.Calls, "\n")), strings.ToLower(w)) {
			r.Pass, r.Why = false, "proposed again what the owner rejected: "+w
			return r
		}
	}
	for _, w := range inc.Expect.NoteContains {
		if !strings.Contains(strings.ToLower(s.Note), strings.ToLower(w)) {
			r.Pass, r.Why = false, "note lacks "+w
			return r
		}
	}
	return r
}

// Run evaluates every incident; runOne builds the Operator for an
// incident (scripted or real model) and runs the session.
func RunEval(ctx context.Context, model string, incidents []Incident, runOne func(ctx context.Context, inc Incident) (Session, error)) EvalReport {
	rep := EvalReport{Model: model, At: time.Now().UTC(), Total: len(incidents)}
	for _, inc := range incidents {
		s, err := runOne(ctx, inc)
		if err != nil {
			rep.Results = append(rep.Results, EvalResult{ID: inc.ID, Why: "session error: " + err.Error()})
			continue
		}
		r := Judge(inc, s)
		if strings.HasPrefix(r.Why, "forbidden tool called") {
			rep.Wrong++
		}
		if len(s.OffMenu) > 0 {
			rep.OffMenu++
		}
		if r.Pass {
			rep.Passed++
		}
		rep.Results = append(rep.Results, r)
	}
	return rep
}

func (r EvalReport) Save(path string) error {
	b, _ := json.MarshalIndent(r, "", "  ")
	return os.WriteFile(path, b, 0o644)
}
