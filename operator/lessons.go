package operator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Lessons (#544): after an incident whose finding went away, lehre's script
// proposes what fixed it as a lesson (skills/lehre/run.py, #839); the owner
// lets it in or not, and a lesson whose later proposals keep failing is
// named in the morning report for removal.

// lesson: a proposal whose only file is a lesson (lessons/<name>.md).
func (p past) lesson() bool {
	return len(p.Files) > 0 && !slices.ContainsFunc(p.Files, func(f string) bool { return !strings.HasPrefix(f, "lessons/") })
}

// sessions reads the Operator's own record, oldest first: one skill's
// sessions ("" every skill's) since a time, those with nothing new left
// out — what lehre's script learns from (#544) and the morning report
// counts (#859, #861). Seeds today's model use after a restart, too.
func (o *Operator) sessions(skill string, since time.Time) []Session {
	if o.Home == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(o.Home, "incidents.jsonl"))
	if err != nil {
		return nil
	}
	var out []Session
	for _, line := range strings.Split(string(b), "\n") {
		var s Session
		if json.Unmarshal([]byte(line), &s) == nil && (skill == "" || s.Skill == skill) && !s.Started.Before(since) && s.Outcome != "unchanged" {
			out = append(out, s)
		}
	}
	return out
}

// Recount seeds each skill's last run and today's model use from the
// record, after a restart (#839, #859).
func (o *Operator) Recount(now time.Time) {
	for _, s := range o.sessions("", time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())) {
		o.work.ran(s)
	}
}
