package operator

import (
	"context"
	"strings"
	"testing"
)

// ADR 0034, amended by ADR 0037: every newer digest is judged by the box's
// model with a short summary of its changelog. modell: kurz waits for no
// one — while the graphics card is elsewhere the script runs anyway, its ask
// answers why, and the image waits for the next night in one line; with the
// household model free, a normal one (here a security fix, so no soak) goes
// to laib as a routine, its summary on the proposal.
func TestVersionenFilesTheCardAlsoWhileTheModelIsBusy(t *testing.T) {
	updates := `[{"service":"adguard","key":"IMAGE","image":"docker.io/adguard/adguardhome","current":"latest","digest":"sha256:a2","ready":"2099-01-01T00:00:00Z",` +
		`"oldVersion":"v0.107.55","newVersion":"v0.107.56","notes":"## v0.107.56\nSecurity: CVE-2026-1"}]`
	for _, c := range []struct{ lease, outcome, want string }{
		{werkstatt, "idle", ""},
		{household, "green", "adguard/IMAGE=Behebt eine Sicherheitslücke."},
	} {
		lease, catalogue := c.lease, loaded
		llm, asked := fakeLease(t, &lease, &catalogue, []Message{{Role: "assistant", Content: "NORMAL\nBehebt eine Sicherheitslücke."}})
		mcp, calls := fakeMCP(t, map[string]string{"laib_updates": updates, "laib_inbox": "[]"})
		op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL + "/v1", Model: "qwen"}, Home: t.TempDir()}
		s, err := op.Run(context.Background(), skillFor(t, Trigger{}, "versionen"), Trigger{Kind: "slot"})
		if err != nil || s.Outcome != c.outcome {
			t.Fatalf("lease %s: %+v %v", c.lease, s, err)
		}
		var proposed string
		for _, call := range *calls {
			if strings.HasPrefix(call, "laib_propose") {
				proposed = call
			}
		}
		if c.want == "" {
			if proposed != "" || !strings.Contains(s.Note, "nächste Nacht") || *asked != 0 {
				t.Fatalf("lease %s: a busy model files nothing and says so (asked %d): %s %s", c.lease, *asked, proposed, s.Note)
			}
			continue
		}
		if !strings.Contains(proposed, `adguard/IMAGE=docker.io/adguard/adguardhome:latest@sha256:a2`) || !strings.Contains(proposed, c.want) || !strings.Contains(proposed, `"routine":"sicherheit"`) {
			t.Fatalf("lease %s: want the digest, the routine and %q: %s", c.lease, c.want, proposed)
		}
	}
}
