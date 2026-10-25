package proposal

import (
	"fmt"
	"strings"
	"testing"
)

// #661: the card says what matters in a few lines — at most six reasons
// and „und N weitere“ — and keeps the rest, and the first single lines
// the review grouped (#687), in the technical details.
func TestCardKeepsImpactShort(t *testing.T) {
	var reasons, lines []string
	for i := range 9 {
		reasons = append(reasons, fmt.Sprintf("Grund %d", i))
	}
	for i := range 126 {
		lines = append(lines, fmt.Sprintf("ändert COUNT_%d=2 in auth/anmeldungen.py, was Laib nicht einschätzen kann", i))
	}
	p := &Proposal{ID: 102, Actor: "laib", Reason: "template update auth", Kind: "template-update", Class: "human", Tier: "apply",
		Files: []string{"services/auth/anmeldungen.py"}, Reasons: reasons, Lines: lines}
	c := Card(p, "dopp.cloud")
	if len(c.Impact) > 7 {
		t.Fatalf("impact has %d lines, want at most 6 and „und N weitere“:\n%s", len(c.Impact), strings.Join(c.Impact, "\n"))
	}
	if last := c.Impact[len(c.Impact)-1]; last != "und 3 weitere (unter Technische Details)" {
		t.Errorf("the last impact line: %q", last)
	}
	for _, w := range []string{"Grund 0", "Grund 5"} {
		if !strings.Contains(strings.Join(c.Impact, "\n"), w) {
			t.Errorf("impact lacks %q", w)
		}
	}
	// #687: the single lines are capped, the diff has them all
	for _, w := range []string{"Grund 6", "Grund 8", "COUNT_0=2", "COUNT_11=2", "und 114 weitere (im Diff)"} {
		if !strings.Contains(c.Details, w) {
			t.Errorf("details lack %q", w)
		}
	}
	if c.Risk != "mittel" || c.Primary != "Später" || c.Decider == "" {
		t.Errorf("weight %s, primary %s, decider %q", c.Risk, c.Primary, c.Decider)
	}
	// six lines stay six: no „und 0 weitere“
	p.Reasons, p.Lines = reasons[:6], nil
	if c := Card(p, "dopp.cloud"); len(c.Impact) != 6 || strings.Contains(strings.Join(c.Impact, "\n"), "weitere") {
		t.Errorf("six reasons: %q", c.Impact)
	}
}
