package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The record (#544): what came of the Operator's proposals, as laib keeps it
// on each inbox entry — effective (the finding went away), no effect,
// rolled back, rejected with the owner's reason. The model is judged by
// what happened, and every session is told.

// past is one inbox entry as the record reads it.
type past struct {
	ID           int       `json:"id"`
	Actor        string    `json:"actor"`
	Reason       string    `json:"reason"`
	Status       string    `json:"status"`
	Created      time.Time `json:"created"`
	Decided      time.Time `json:"decided"`
	Files        []string  `json:"files"`
	Finding      string    `json:"finding"`
	Outcome      string    `json:"outcome"`
	RejectReason string    `json:"rejectReason"`
}

// counted: the outcomes that say whether a proposal was good. "later" (not
// now) and "applied" (nothing to measure) are neither.
var counted = []string{"effective", "no-effect", "rolled-back", "rejected"}

var outcomeWord = map[string]string{"effective": "wirksam", "no-effect": "ohne Wirkung", "rolled-back": "zurückgenommen", "rejected": "abgelehnt"}

// history reads every inbox entry with the ops hand's own token; the record
// is the box's, not a skill's tool.
func (o *Operator) history(ctx context.Context) []past {
	text, isErr, err := o.MCP.Call(ctx, "laib_inbox", map[string]any{"args": "--all"})
	var all []past
	if err != nil || isErr || json.Unmarshal([]byte(text), &all) != nil {
		return nil
	}
	return all
}

// mine: the Operator's own proposals, newest first.
func mine(all []past) []past {
	var out []past
	for _, p := range all {
		if strings.SplitN(p.Actor, ":", 2)[0] == "ops" {
			out = append(out, p)
		}
	}
	return out
}

// tally counts the outcomes of ps; the reasons of the rejected come along.
func tally(ps []past) (n int, by map[string]int, why []string) {
	by = map[string]int{}
	for _, p := range ps {
		if !slices.Contains(counted, p.Outcome) {
			continue
		}
		n++
		by[p.Outcome]++
		if p.Outcome == "rejected" && p.RejectReason != "" {
			why = append(why, fmt.Sprintf("#%d „%s“", p.ID, p.RejectReason))
		}
	}
	return n, by, why
}

func words(by map[string]int) string {
	var parts []string
	for _, k := range counted {
		if by[k] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", by[k], outcomeWord[k]))
		}
	}
	return strings.Join(parts, ", ")
}

// Record is the context line every skill session starts with: the last ten
// measured proposals, with the reasons of the rejected ones.
func Record(all []past) string {
	var last []past
	for _, p := range mine(all) {
		if slices.Contains(counted, p.Outcome) && len(last) < 10 {
			last = append(last, p)
		}
	}
	n, by, why := tally(last)
	if n == 0 {
		return ""
	}
	out := fmt.Sprintf("Deine letzten %d Vorschläge: %s.", n, words(by))
	if len(why) > 0 {
		out += " Abgelehnt, mit Grund: " + strings.Join(why, "; ") + "."
	}
	return out + " Schlag nichts vor, was so schon abgelehnt wurde oder ohne Wirkung blieb.\n"
}

// Rate is the morning report's line (#544): the last 30 days' rate.
func Rate(all []past, now time.Time) string {
	var month []past
	for _, p := range mine(all) {
		if now.Sub(p.Created) <= 30*24*time.Hour {
			month = append(month, p)
		}
	}
	n, by, _ := tally(month)
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("Vorschläge des Operators, 30 Tage: %d von %d wirksam (%d %%); %s.", by["effective"], n, by["effective"]*100/n, words(by))
}

// proposalID reads the number of the proposal a propose or repair call
// filed from its answer (JSON over MCP, or the sentence).
var proposalID = regexp.MustCompile(`(?:"id":\s*|#)([0-9]+)`)
