package operator

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

// #792: three failed check units stayed red for hours, unseen. A failed
// oneshot writes no red journal line, so no incident opened, and every
// session waited for a model the router had unloaded. Now a look's red
// opens an incident, its first steps need no model, and an incident that
// cannot reach the model is escalated after incidentWait.
const incidentWait = 30 * time.Minute

// Work is what the Operator does now, for /ready and the Box page: the
// incident, since when, its last step, why it waits, how many wait.
type Work struct {
	Incident string         `json:"incident,omitempty"`
	Since    time.Time      `json:"since,omitzero"`
	Step     string         `json:"step,omitempty"`
	Waiting  string         `json:"waiting,omitempty"`
	Queued   int            `json:"queued,omitempty"`
	Skills   map[string]Ran `json:"skills,omitempty"` // each skill's last run (#839)
}

// Ran is a skill's last run as /ready and the Box page show it (#839):
// when, how it ended and why, and what it caused.
type Ran struct {
	At      time.Time `json:"at"`
	Outcome string    `json:"outcome"`
	Why     string    `json:"why,omitempty"`
	Caused  string    `json:"caused,omitempty"`
}

type work struct {
	mu sync.Mutex
	w  Work
}

// ran keeps a session as its skill's last run.
func (k *work) ran(s Session) {
	k.mu.Lock()
	defer k.mu.Unlock()
	var c []string
	if s.Note != "" && s.Outcome != "error" {
		c = append(c, "Notiz")
	}
	for _, n := range s.Proposed {
		c = append(c, fmt.Sprintf("#%d", n))
	}
	for v, word := range map[string]string{"laib_restart": "Neustart", "laib_wish": "Wunsch", "laib_escalate": "Eskalation"} {
		if n := countPrefix(slices.Concat(s.Calls, s.Script), v); n > 0 {
			c = append(c, fmt.Sprintf("%d× %s", n, word))
		}
	}
	if len(s.Refused) > 0 {
		c = append(c, fmt.Sprintf("%d verweigert", len(s.Refused)))
	}
	slices.Sort(c)
	if k.w.Skills == nil {
		k.w.Skills = map[string]Ran{}
	}
	k.w.Skills[s.Skill] = Ran{At: s.Started, Outcome: s.Outcome, Why: clip(s.Why), Caused: strings.Join(c, ", ")}
}

func (k *work) begin(incident string, at time.Time) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.w.Incident, k.w.Since, k.w.Step = clip(incident), at, ""
}

func (k *work) step(s string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.w.Step = s
}

func (k *work) wait(why string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.w.Waiting = why
}

// Work is what the Operator works on now, else the oldest incident that
// waits, and why the last session had to wait.
func (o *Operator) Work() Work {
	o.work.mu.Lock()
	w := o.work.w
	o.work.mu.Unlock()
	ws := o.waiting()
	if w.Queued = len(ws); w.Incident == "" && len(ws) > 0 {
		w.Incident, w.Since, w.Step = clip(ws[0].Trigger.Detail), time.Unix(ws[0].Trigger.Queued, 0).UTC(), "wartet in der Warteschlange"
	}
	return w
}

func clip(s string) string {
	if r := []rune(s); len(r) > 200 {
		return string(r[:200]) + "…"
	}
	return s
}

// services are the incident's services: its own and the ones to restart, at most three.
func (t Trigger) services() []string {
	var out []string
	for _, s := range append([]string{t.Service}, t.Restart...) {
		if s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out[:min(len(out), 3)]
}

// open turns a look's new reds into one incident, as a cascade within a
// minute is one (#287). A red is opened once — memory.Repaired marks it,
// and forgets it when the red goes away — so its return is a new incident.
func (o *Operator) open(fs []finding, m *memory) []Trigger {
	var tr *Trigger
	for _, f := range fs {
		id := f.id()
		if f.Severity != "rot" || f.Service == "" || f.Probe == "operator" || m.Repaired[id] != "" {
			continue
		}
		m.Repaired[id] = "vorfall"
		what := f.Service + ": " + f.Probe + " " + f.Detail
		if tr == nil {
			tr = &Trigger{Kind: "event", Service: f.Service, Detail: what, Opened: true}
		} else {
			tr.Detail += "; " + what
		}
		if len(f.Repair) == 2 && f.Repair[0] == "restart" {
			tr.Restart = append(tr.Restart, f.Repair[1])
		}
	}
	if tr == nil {
		return nil
	}
	if strings.Contains(tr.Detail, "; ") {
		tr.Detail = "mehrere Dienste rot — " + tr.Detail
	}
	o.remember(m)
	return []Trigger{*tr}
}

// said is what the first aid saw in the log and did, as sentences.
func (t Trigger) said() string {
	s := ""
	if t.Saw != "" {
		s += " Im Log: " + t.Saw + "."
	}
	if t.Done != "" {
		s += " Schon getan: " + t.Done + "."
	}
	return s
}

// stillRed is which of the services diag shows red now; known is false
// when diag could not be read.
func (o *Operator) stillRed(ctx context.Context, mcp *MCP, svcs []string) ([]string, bool) {
	text, isErr, err := mcp.Call(ctx, "laib_diag", map[string]any{})
	fs, _, ok := parseDiag(text)
	if err != nil || isErr || !ok {
		return nil, false
	}
	var red []string
	for _, f := range fs {
		if f.Severity == "rot" && slices.Contains(svcs, f.Service) && !slices.Contains(red, f.Service) {
			red = append(red, f.Service)
		}
	}
	return red, true
}

// escalate hands an incident that waited incidentWait to the next hand,
// once, for each service still red, with what was seen and why it waited;
// it stays queued for the model. One no longer red is dropped.
func (o *Operator) escalate(ctx context.Context, w waiting, why string) {
	mcp := *o.MCP
	mcp.Skill = w.Skill
	tr := w.Trigger
	red, known := o.stillRed(ctx, &mcp, tr.services())
	if known && len(red) == 0 {
		o.drop(ctx, w, "nicht mehr rot")
		return
	}
	if !known {
		red = tr.services()
	}
	summary := fmt.Sprintf("Vorfall seit %s, %d× gemeldet: %s.%s", time.Unix(tr.Queued, 0).Format("15:04"), max(tr.Count, 1), clip(tr.Detail), tr.said())
	summary += fmt.Sprintf(" Das Modell war %d Minuten lang nicht zu haben: %s.", int(incidentWait.Minutes()), cmp.Or(why, "die Sitzungen scheiterten"))
	for _, svc := range red {
		mcp.Call(ctx, "laib_escalate", map[string]any{"service": svc, "summary": summary})
	}
	w.Trigger.Escalated = true
	if b, err := json.Marshal(w); err == nil {
		os.WriteFile(w.path, b, 0o644)
	}
}

// lastLine is the last line of a text that says something, cut to 200 characters.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return clip(strings.TrimSpace(lines[len(lines)-1]))
}
