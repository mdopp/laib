package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	Last     map[string]time.Time
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

// Step runs every active scheduled skill that is due at now. An interval
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
	var out []Session
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
		kind := "tick"
		if sk.Output == "report" {
			kind = "report"
		}
		s := r.RunNow(ctx, sk, Trigger{Kind: kind})
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
	return out
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
