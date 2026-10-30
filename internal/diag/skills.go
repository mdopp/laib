package diag

import (
	"fmt"
	"slices"
	"strings"

	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/skill"
)

// skillFindings (#543): an active skill whose own tools the policy refuses
// leaves a box duty undone — sicherung without backup runs no restore test,
// nutzung without wish proposes nothing — and only `laib skill list` said
// so. One finding per skill, for the owner, whose choice it is: grant the
// verb, rest the skill, or replace it with a file without that tool. A
// capable model's more: tools are extras, not the skill's job.
// An open proposal that grants a tool (the update run's, #932, or one filed
// with the command below) is named instead of the command.
func skillFindings(sks []skill.Skill, open ...*proposal.Proposal) []Finding {
	var out []Finding
	for _, s := range sks {
		if s.Status != "active" {
			continue
		}
		var tools, whys []string
		for _, t := range s.Tools {
			why := s.Denied[t]
			if why == "" || strings.HasPrefix(why, "kein MCP") {
				continue // in reach, or the skill file's own mistake
			}
			tools = append(tools, t)
			if !slices.Contains(whys, why) {
				whys = append(whys, why)
			}
		}
		if len(tools) == 0 {
			continue
		}
		list := strings.Join(tools, ", ")
		// the grant is the owner's decision in the Inbox (#575): diag names
		// the command that proposes it and grants nothing itself
		var grants []string
		for _, t := range tools {
			cmd := fmt.Sprintf("laib propose --grant ops %s \"Fähigkeit %s braucht %s\"", t, s.Name, t)
			for _, p := range open {
				if p.Status == "open" && (strings.Contains(p.Reason, "+"+t+" (Fähigkeit ") || strings.Contains(p.Reason, "Fähigkeit "+s.Name+" braucht "+t)) {
					cmd = fmt.Sprintf("#%d gibt ops %s, wartet auf deine Freigabe in der Inbox", p.ID, t)
				}
			}
			grants = append(grants, cmd)
		}
		out = append(out, Finding{Severity: "gelb", Probe: "skill",
			Detail: fmt.Sprintf("Fähigkeit %s läuft ohne %s, ihre Aufgabe bleibt liegen (%s): %s", s.Name, list, strings.TrimSuffix(s.Description, "."), strings.Join(whys, "; ")),
			Fix:    fmt.Sprintf("deine Wahl: %s, oder laib skill pause %s, oder eine skills/%s/skill.yml im State-Repo ohne %s", strings.Join(grants, " und "), s.Name, s.Name, list)})
	}
	return out
}
