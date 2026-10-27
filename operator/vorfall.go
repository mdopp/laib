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
	Incident string    `json:"incident,omitempty"`
	Since    time.Time `json:"since,omitzero"`
	Step     string    `json:"step,omitempty"`
	Waiting  string    `json:"waiting,omitempty"`
	Queued   int       `json:"queued,omitempty"`
}

type work struct {
	mu sync.Mutex
	w  Work
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

// firstAid is an opened incident's first steps, without a model: each
// service's last log line, the restart diag offers where it is granted,
// and whether it is still red; then a note in German says what it saw and
// did, and what comes next. It returns whether nothing is red any more.
func (o *Operator) firstAid(ctx context.Context, mcp *MCP, sk Skill, tr *Trigger, now time.Time) (bool, string, []string) {
	tr.Aided = true
	var calls, done []string
	do := func(step, tool string, args map[string]any) (string, bool) {
		o.work.step(step)
		calls = append(calls, tool+" "+shownArgs(args))
		out, isErr, err := mcp.Call(ctx, tool, args)
		return cmp.Or(out, fmt.Sprint(err)), err == nil && !isErr
	}
	for _, svc := range tr.services() {
		if slices.Contains(sk.Granted, "logs") {
			if out, ok := do("liest die Logs von "+svc, "laib_logs", map[string]any{"service": svc, "since": "1h"}); ok && lastLine(out) != "" {
				tr.Saw = strings.TrimSpace(tr.Saw + " " + svc + ": „" + lastLine(out) + "“")
			}
		}
		if slices.Contains(sk.Granted, "restart") && slices.Contains(tr.Restart, svc) {
			out, _ := do("startet "+svc+" neu", "laib_restart", map[string]any{"service": svc})
			done = append(done, svc+" neu gestartet ("+firstLine(cmp.Or(out, "ohne Antwort"))+")")
		}
	}
	tr.Done = cmp.Or(strings.Join(done, ", "), "Logs gelesen")
	fixed := false
	if len(done) > 0 && slices.Contains(sk.Granted, "diag") {
		o.work.step("prüft, ob es wieder grün ist")
		calls = append(calls, "laib_diag {}")
		red, known := o.stillRed(ctx, mcp, tr.services())
		fixed = known && len(red) == 0
	}
	text := fmt.Sprintf("Vorfall %s: %s.%s", now.Format("15:04"), tr.Detail, tr.said())
	if fixed {
		text += " Danach ist nichts davon mehr rot."
	} else {
		text += fmt.Sprintf(" Weiter: das Modell sieht nach; ist es um %s noch rot, eskaliere ich.", now.Add(incidentWait).Format("15:04"))
	}
	mcp.Call(ctx, "laib_note", map[string]any{"kind": "incident", "text": text})
	return fixed, text, calls
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
