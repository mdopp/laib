package operator

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Skill is one of the Operator's skills as the kernel lists it (laib skill
// list, ADR 0023): the file plus the kernel's verdict — its status and the
// tools it is granted. The Operator runs skills; it never widens them.
type Skill struct {
	Name        string         `json:"name"`
	Source      string         `json:"source"`
	Description string         `json:"description"`
	Trigger     SkillTrigger   `json:"trigger"`
	Tools       []string       `json:"tools"`
	More        []string       `json:"more"` // only for a capable model (#286)
	Context     string         `json:"context"`
	Output      string         `json:"output"`
	Prompt      string         `json:"prompt"`
	Limits      map[string]int `json:"limits"`  // tool → calls per session (#376)
	Updates     string         `json:"updates"` // patch: the runner reads updates and offers only those (#376)
	Lessons     string         `json:"lessons"` // vorfall: the runner proposes lessons, no model (#544)
	Run         string         `json:"run"`     // run.py: the skill's script, its deterministic steps (#839)
	Modell      string         `json:"modell"`  // frei (default) | urteil | nein: where the model decides (#839)
	Status      string         `json:"status"`
	Granted     []string       `json:"granted"`
}

// SkillTrigger is when a skill runs: exactly one field is set.
type SkillTrigger struct {
	Every  string `json:"every,omitempty"`
	Daily  string `json:"daily,omitempty"`
	Weekly string `json:"weekly,omitempty"`
	Event  string `json:"event,omitempty"`
}

// Skills asks the kernel for the registry.
func (m *MCP) Skills(ctx context.Context) ([]Skill, error) {
	text, isErr, err := m.Call(ctx, "laib_skill", map[string]any{"action": "list"})
	if err != nil || isErr {
		return nil, fmt.Errorf("laib skill list: %v %s", err, firstLine(text))
	}
	var out []Skill
	return out, json.Unmarshal([]byte(text), &out)
}

// verdicts are what a judgement writes (modell: urteil, #839): the script
// did the reading, the model decides what comes of it.
var verdicts = []string{"note", "wish", "propose", "repair", "escalate"}

// menu is what the model is offered of the granted tools: a small model
// keeps the short menu, the skill's more tools are for a capable one
// (#286); a judgement (modell: urteil, #839) gets only what writes its
// verdict, the script read.
func (sk Skill) menu(typed bool) map[string]bool {
	m := map[string]bool{}
	for _, g := range sk.Granted {
		if (!typed || !slices.Contains(sk.More, g) || slices.Contains(sk.Tools, g)) && (sk.Modell != "urteil" || slices.Contains(verdicts, g)) {
			m["laib_"+g] = true
		}
	}
	return m
}

var weekdays = map[string]int{"Mo": 0, "Di": 1, "Mi": 2, "Do": 3, "Fr": 4, "Sa": 5, "So": 6}

// Due says whether a scheduled skill's slot has come since its last run:
// every — an interval after the last run; daily — today's time; weekly —
// this week's day and time. An event skill is never due by the clock.
// tick and reportAt stand in for ${TICK} and ${REPORT_AT}.
func Due(t SkillTrigger, now, last time.Time, tick, reportAt string) bool {
	expand := strings.NewReplacer("${TICK}", tick, "${REPORT_AT}", reportAt).Replace
	clock := func(hm string) (time.Duration, bool) {
		c, err := time.Parse("15:04", hm)
		return time.Duration(c.Hour())*time.Hour + time.Duration(c.Minute())*time.Minute, err == nil
	}
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	var slot time.Time
	switch {
	case t.Every != "":
		d, err := time.ParseDuration(expand(t.Every))
		return err == nil && d > 0 && now.Sub(last) >= d
	case t.Daily != "":
		off, ok := clock(expand(t.Daily))
		if !ok {
			return false
		}
		slot = day.Add(off)
	case t.Weekly != "":
		wd, hm, _ := strings.Cut(t.Weekly, " ")
		n, known := weekdays[wd]
		off, ok := clock(hm)
		if !known || !ok {
			return false
		}
		monday := day.AddDate(0, 0, -((int(now.Weekday()) + 6) % 7))
		slot = monday.AddDate(0, 0, n).Add(off)
	default:
		return false
	}
	return !now.Before(slot) && last.Before(slot)
}

// Runner runs the skills whose time has come, one session at a time, and
// remembers each skill's last run under OPERATOR_HOME, so a restart does
// not repeat a daily or weekly one.
type Runner struct {
	Op       *Operator
	Tick     string // OPERATOR_TICK, for ${TICK}; default 10m
	ReportAt string // OPERATOR_REPORT_AT, for ${REPORT_AT}; default 07:00
	EvalAt   string // OPERATOR_EVAL_AT: when the nightly eval may start (#860); default 03:30
	Last     map[string]time.Time
	told     map[string]bool // the slots whose waiting or failing is journaled (#858)
	mu       sync.Mutex
}

func (r *Runner) lastPath() string {
	if r.Op.Home == "" {
		return ""
	}
	return filepath.Join(r.Op.Home, "last-run.json")
}

// LoadLast reads the last runs; a missing file is a first start.
func (r *Runner) LoadLast() {
	r.Last = map[string]time.Time{}
	if p := r.lastPath(); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			json.Unmarshal(b, &r.Last)
		}
	}
}

func (r *Runner) saveLast() {
	if p := r.lastPath(); p != "" {
		os.MkdirAll(r.Op.Home, 0o755)
		b, _ := json.Marshal(r.Last)
		os.WriteFile(p, b, 0o644)
	}
}

// Step runs the incidents that waited, then every active scheduled skill
// that is due at now. An interval
// skill seen for the first time starts its clock: its first run is one
// interval later, as the tick always did.
func (r *Runner) Step(ctx context.Context, now time.Time, skills []Skill) []Session {
	tick, reportAt := r.Tick, r.ReportAt
	if tick == "" {
		tick = "10m"
	}
	if reportAt == "" {
		reportAt = "07:00"
	}
	out := r.drain(ctx, now, skills) // the incidents that waited for the graphics card first (#550)
	r.nightly(ctx, now, skills)
	var opened []Trigger
	for _, sk := range skills {
		if sk.Status != "active" || sk.Trigger.Event != "" {
			continue
		}
		last, seen := r.Last[sk.Name]
		if !seen && sk.Trigger.Every != "" {
			r.Last[sk.Name] = now
			continue
		}
		if !Due(sk.Trigger, now, last, tick, reportAt) {
			continue
		}
		r.Last[sk.Name] = now
		r.saveLast()
		// a daily or weekly slot may wake an unloaded model, a look does not (#858)
		kind := "slot"
		if sk.Output == "report" {
			kind = "report"
		} else if sk.Trigger.Every != "" {
			kind = "tick"
		}
		s := r.RunNow(ctx, sk, Trigger{Kind: kind})
		opened = append(opened, s.opened...)
		r.trace(ctx, sk, s)
		if s.Outcome == "skipped" && sk.Trigger.Every == "" {
			// a daily or weekly slot is not lost to a busy graphics card (#550):
			// the next step tries again; an interval skill keeps its interval
			if r.Last[sk.Name] = last; !seen {
				delete(r.Last, sk.Name)
			}
			r.saveLast()
		}
		out = append(out, s)
	}
	for _, tr := range opened { // a look's new reds are incidents (#792)
		out = append(out, r.Event(ctx, skills, tr)...)
	}
	return out
}

// trace is a slot that waits or fails, one journal line with its reason
// (#858): once while it keeps waiting or failing, again after it ran.
func (r *Runner) trace(ctx context.Context, sk Skill, s Session) {
	if r.told == nil {
		r.told = map[string]bool{}
	}
	if s.Outcome != "skipped" && s.Outcome != "error" {
		delete(r.told, sk.Name)
		return
	}
	if r.told[sk.Name] {
		return
	}
	r.told[sk.Name] = true
	text := fmt.Sprintf("Die Fähigkeit %s wartet: %s. Laib versucht es wieder, bis es läuft.", sk.Name, s.Why)
	if s.Outcome == "error" {
		text = fmt.Sprintf("Die Fähigkeit %s ist gescheitert: %s. Laib versucht es zum nächsten Termin wieder.", sk.Name, firstLine(s.Note))
	}
	mcp := *r.Op.MCP
	mcp.Skill = sk.Name
	mcp.Call(ctx, "laib_note", map[string]any{"kind": "watch", "text": text})
}

// Event runs every active event skill for a red.
func (r *Runner) Event(ctx context.Context, skills []Skill, tr Trigger) []Session {
	var out []Session
	for _, sk := range skills {
		if sk.Status == "active" && sk.Trigger.Event == "red" {
			out = append(out, r.RunNow(ctx, sk, tr))
		}
	}
	return out
}

// RunNow runs one session of a skill, after any session still running.
func (r *Runner) RunNow(ctx context.Context, sk Skill, tr Trigger) Session {
	r.mu.Lock() // one session at a time; the envelope's budget is per hour anyway
	defer r.mu.Unlock()
	s, err := r.Op.Run(ctx, sk, tr)
	if err != nil {
		s.Note = err.Error()
	}
	return s
}

// nightly runs the eval once a night (#860), from EvalAt on, while the
// household model is free; a run that had to wait is tried at the next
// step. Then the apply gate's state goes to the journal, once each.
func (r *Runner) nightly(ctx context.Context, now time.Time, skills []Skill) {
	if r.Op.Evals == "" {
		return
	}
	defer func() {
		if _, say := r.Op.applyGate(); say != r.Op.gate {
			if r.Op.gate = say; say != "" {
				r.Op.MCP.Call(ctx, "laib_note", map[string]any{"kind": "watch", "text": say})
			}
		}
	}()
	last := r.Last["eval"]
	if len(skills) == 0 || !Due(SkillTrigger{Daily: cmp.Or(r.EvalAt, "03:30")}, now, last, "", "") || r.Op.LLM.Busy(ctx, true) != "" {
		return
	}
	r.Last["eval"] = now
	if _, err := r.Eval(ctx, skills); errors.Is(err, errWaited) {
		r.Last["eval"] = last
	} else if err != nil {
		r.Op.MCP.Call(ctx, "laib_note", map[string]any{"kind": "watch", "text": "Das nächtliche Eval lief nicht: " + firstLine(err.Error()) + "."})
	}
	r.saveLast()
}
