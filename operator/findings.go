package operator

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// What the runner remembers of diag (#543): 131 notes in 72 hours, every
// one repeating the same yellow findings, and not one proposal. Whether a
// look found something new is code, not prompt (CLAUDE.md rule 1).

// finding is one laib diag result as the runner reads it.
type finding struct {
	Severity string   `json:"severity"`
	Probe    string   `json:"probe"`
	Service  string   `json:"service"`
	Detail   string   `json:"detail"`
	Repair   []string `json:"repair"`
}

var digits = regexp.MustCompile(`[0-9]+`)

// id is a finding's identity: severity, probe, service and the detail with
// its numbers taken out — an age ("vor 3 h"), a percentage or a count moves
// on every look and is not a new finding.
func (f finding) id() string {
	return f.Severity + " " + f.Probe + " " + f.Service + " " + digits.ReplaceAllString(f.Detail, "#")
}

// label is what a note names a finding by, from its id.
func label(id string) string {
	p := strings.SplitN(id, " ", 4)
	if len(p) < 3 {
		return id
	}
	return strings.TrimSpace(p[1] + " " + p[2])
}

// parseDiag reads laib diag's JSON: the findings and each one's raw form,
// fix included, for the model.
func parseDiag(text string) ([]finding, []json.RawMessage, bool) {
	var raw []json.RawMessage
	if json.Unmarshal([]byte(text), &raw) != nil {
		return nil, nil, false
	}
	fs := make([]finding, len(raw))
	for i, r := range raw {
		if json.Unmarshal(r, &fs[i]) != nil {
			return nil, nil, false
		}
	}
	return fs, raw, true
}

// ids is the findings' identities, sorted and once each; never nil, so a
// look that found nothing is remembered as such.
func ids(fs []finding, more ...string) []string {
	out := append([]string{}, more...)
	for _, f := range fs {
		out = append(out, f.id())
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// memory lives under OPERATOR_HOME (findings.json): per skill the findings
// its last session that ended without an error saw, and per finding what
// came of the repair the runner filed for it.
type memory struct {
	Seen     map[string][]string `json:"seen"`
	Repaired map[string]string   `json:"repaired"`
}

func (o *Operator) memory() *memory {
	m := &memory{}
	if o.Home != "" {
		if b, err := os.ReadFile(filepath.Join(o.Home, "findings.json")); err == nil {
			json.Unmarshal(b, m)
		}
	}
	if m.Seen == nil {
		m.Seen = map[string][]string{}
	}
	if m.Repaired == nil {
		m.Repaired = map[string]string{}
	}
	return m
}

func (o *Operator) remember(m *memory) {
	if o.Home == "" {
		return
	}
	os.MkdirAll(o.Home, 0o755)
	b, _ := json.Marshal(m)
	os.WriteFile(filepath.Join(o.Home, "findings.json"), b, 0o644)
}

// news is a diag result as the model sees it: what the skill's last session
// already saw drops out, red excepted — red is repeated until it is gone —
// and two lines say what was left out and what is no longer there. So a
// yellow the owner alone can fix (the RAID mirror, the watchdog, the NAS)
// is in one note and one report, not in every one.
func news(fs []finding, raw []json.RawMessage, seen []string) string {
	if seen == nil {
		b, _ := json.Marshal(raw)
		return string(b)
	}
	keep, known := []json.RawMessage{}, []string{}
	for i, f := range fs {
		if f.Severity != "rot" && slices.Contains(seen, f.id()) {
			known = append(known, label(f.id()))
			continue
		}
		keep = append(keep, raw[i])
	}
	b, _ := json.Marshal(keep)
	out := string(b)
	if len(known) > 0 {
		out += fmt.Sprintf("\nSchon gemeldet und unverändert, nicht wiederholen: %d %s (%s).", len(known), map[bool]string{true: "Befund", false: "Befunde"}[len(known) == 1], strings.Join(known, ", "))
	}
	now := ids(fs)
	var gone []string
	for _, id := range seen {
		if !slices.Contains(now, id) {
			gone = append(gone, label(id))
		}
	}
	if len(gone) > 0 {
		out += "\nSeit der letzten Sitzung nicht mehr da: " + strings.Join(gone, ", ") + "."
	}
	return out
}

// repairs files laib repair for every finding that is not red and carries a
// repair verb (#543): diag knows the fix, a human approves it. Red is the
// incident session's. A finding is filed once: not again while its
// proposal is open, nor after a no, until the finding changes or goes away.
// The result is the note's line for the owner.
func (o *Operator) repairs(ctx context.Context, mcp *MCP, fs []finding, m *memory) string {
	var h strings.Builder
	now := ids(fs)
	for id := range m.Repaired {
		if !slices.Contains(now, id) {
			delete(m.Repaired, id)
		}
	}
	for _, f := range fs {
		id := f.id()
		if f.Severity == "rot" || len(f.Repair) == 0 || m.Repaired[id] != "" {
			continue
		}
		args := map[string]any{"probe": f.Probe}
		if f.Service != "" {
			args["service"] = f.Service
		}
		cmdline := "laib " + strings.Join(f.Repair, " ")
		out, isErr, err := mcp.Call(ctx, "laib_repair", args)
		switch {
		case err != nil:
			continue // laib away: the next look tries again
		case isErr:
			m.Repaired[id] = "refused"
			fmt.Fprintf(&h, " %s: %s ließ sich nicht vorschlagen: %s.", label(id), cmdline, firstLine(cmp.Or(out, "ohne Grund")))
		default:
			m.Repaired[id] = "proposed"
			fmt.Fprintf(&h, " %s: Reparatur %s vorgeschlagen, für einen Menschen.", label(id), cmdline)
		}
	}
	o.remember(m)
	return strings.TrimSpace(h.String())
}
