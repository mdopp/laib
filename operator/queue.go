package operator

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The incident queue (#550, owner 2026-10-24): an incident that comes while
// the graphics card is lent, loading or right after a person's request
// waits instead of being skipped, and the next step after the lease frees
// runs it. One file per incident in OPERATOR_HOME/queue, so a restart keeps
// it; the same finding — skill, service, the detail without its numbers —
// is one file that counts its reds.
const (
	queueMax = 20             // incidents waiting; the oldest gives way
	queueAge = 24 * time.Hour // older than this it is not run: the next look sees what is still red
)

type waiting struct {
	Skill   string  `json:"skill"`
	Trigger Trigger `json:"trigger"`
	path    string
}

var unsafeName = regexp.MustCompile(`[^a-z0-9-]+`)

// waited is the note's line for an incident that waited, or "".
func (t Trigger) waited(now time.Time) string {
	if t.Queued == 0 {
		return ""
	}
	since := time.Unix(t.Queued, 0)
	d := cmp.Or(strings.TrimSuffix(now.Sub(since).Round(time.Minute).String(), "0s"), "0m")
	return fmt.Sprintf("Der Vorfall wartete %s auf die Grafikkarte (seit %s, %d× gemeldet).", d, since.Format("15:04"), max(t.Count, 1))
}

// enqueue puts an incident into the queue, or counts it on the one waiting
// for the same finding; a replay that is skipped again keeps its place.
func (o *Operator) enqueue(ctx context.Context, skill string, tr Trigger, now time.Time) {
	dir := filepath.Join(o.Home, "queue")
	os.MkdirAll(dir, 0o755)
	key := sha256.Sum256([]byte(skill + "\n" + tr.Service + "\n" + digits.ReplaceAllString(tr.Detail, "#")))
	path := filepath.Join(dir, fmt.Sprintf("%s-%s-%x.json", skill, unsafeName.ReplaceAllString(tr.Service, "_"), key[:4]))
	w := waiting{Skill: skill, Trigger: tr}
	if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &w) == nil {
		if tr.Queued != 0 {
			return
		}
		w.Trigger.Count++
		w.Trigger.Detail = tr.Detail
	} else if w.Trigger.Queued == 0 {
		w.Trigger.Queued, w.Trigger.Count = now.Unix(), 1
	}
	b, _ := json.Marshal(w)
	if os.WriteFile(path+".tmp", b, 0o644) != nil || os.Rename(path+".tmp", path) != nil {
		return
	}
	if ws := o.waiting(); len(ws) > queueMax {
		for _, w := range ws[:len(ws)-queueMax] {
			o.drop(ctx, w, fmt.Sprintf("die Warteschlange ist voll (%d)", queueMax))
		}
	}
}

// waiting is the queue, oldest first.
func (o *Operator) waiting() []waiting {
	dir := filepath.Join(o.Home, "queue")
	names, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	var ws []waiting
	for _, p := range names {
		var w waiting
		if b, err := os.ReadFile(p); err == nil && json.Unmarshal(b, &w) == nil {
			w.path = p
			ws = append(ws, w)
		}
	}
	slices.SortStableFunc(ws, func(a, b waiting) int { return cmp.Compare(a.Trigger.Queued, b.Trigger.Queued) })
	return ws
}

// drop takes an incident out of the queue unrun and journals it.
func (o *Operator) drop(ctx context.Context, w waiting, why string) {
	os.Remove(w.path)
	what := []rune(cmp.Or(w.Trigger.Detail, w.Trigger.Service))
	if len(what) > 200 {
		what = append(what[:200], '…')
	}
	mcp := *o.MCP
	mcp.Skill = w.Skill
	text := fmt.Sprintf("Vorfall nicht nachgeholt (%s): %s. Er wartete seit %s auf die Grafikkarte, %d× gemeldet; was noch rot ist, findet der nächste Blick.",
		string(what), why, time.Unix(w.Trigger.Queued, 0).Format("02.01. 15:04"), max(w.Trigger.Count, 1))
	mcp.Call(ctx, "laib_note", map[string]any{"kind": "watch", "text": text})
}

// drain runs the queue at a step, oldest first, once the model is free; an
// incident older than queueAge or of a skill no longer active is dropped.
func (r *Runner) drain(ctx context.Context, now time.Time, skills []Skill) []Session {
	ws := r.Op.waiting()
	if len(ws) == 0 || len(skills) == 0 { // no list while laib is away: nothing is judged
		return nil
	}
	busy := r.Op.LLM.Busy(ctx) != ""
	var out []Session
	for _, w := range ws {
		i := slices.IndexFunc(skills, func(sk Skill) bool {
			return sk.Name == w.Skill && sk.Status == "active" && sk.Trigger.Event != ""
		})
		switch {
		case now.Sub(time.Unix(w.Trigger.Queued, 0)) >= queueAge:
			r.Op.drop(ctx, w, "älter als 24 Stunden")
		case i < 0:
			r.Op.drop(ctx, w, "die Fähigkeit "+w.Skill+" ist nicht aktiv")
		case !busy:
			s := r.RunNow(ctx, skills[i], w.Trigger)
			if s.Outcome != "skipped" {
				os.Remove(w.path)
			}
			out = append(out, s)
		}
	}
	return out
}
