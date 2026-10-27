package operator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Lessons (#544): after an incident whose finding went away, lehre's script
// proposes what fixed it as a lesson (skills/lehre/run.py, #839); the owner
// lets it in or not, and a lesson whose later proposals keep failing is
// named in the morning report for removal.

// lesson: a proposal whose only file is a lesson (lessons/<name>.md).
func (p past) lesson() bool {
	return len(p.Files) > 0 && !slices.ContainsFunc(p.Files, func(f string) bool { return !strings.HasPrefix(f, "lessons/") })
}

// sessions reads the Operator's own record of one skill's sessions, oldest
// first: what lehre's script learns from (#544).
func (o *Operator) sessions(skill string) []Session {
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
		if json.Unmarshal([]byte(line), &s) == nil && s.Skill == skill {
			out = append(out, s)
		}
	}
	return out
}
