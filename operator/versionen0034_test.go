package operator

import (
	"context"
	"strings"
	"testing"
)

// ADR 0034, the owner's choice (c): every newer digest is one card a human
// taps, with the model's short summary of its changelog. modell: kurz
// waits for no one — while the graphics card is elsewhere the script runs
// anyway, its ask answers why, and the card says so in one line; with the
// household model free, the summary is on the proposal.
func TestVersionenFilesTheCardAlsoWhileTheModelIsBusy(t *testing.T) {
	updates := `[{"service":"adguard","key":"IMAGE","image":"docker.io/adguard/adguardhome","current":"latest","digest":"sha256:a2",` +
		`"oldVersion":"v0.107.55","newVersion":"v0.107.56","notes":"## v0.107.56\nSecurity: CVE-2026-1"}]`
	for _, c := range []struct{ lease, want string }{
		{werkstatt, "adguard/IMAGE=Keine Zusammenfassung: das Modell war gerade beschäftigt."},
		{household, "adguard/IMAGE=Behebt eine Sicherheitslücke."},
	} {
		lease, catalogue := c.lease, loaded
		llm, asked := fakeLease(t, &lease, &catalogue, []Message{{Role: "assistant", Content: "Behebt eine Sicherheitslücke."}})
		mcp, calls := fakeMCP(t, map[string]string{"laib_updates": updates, "laib_inbox": "[]"})
		op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL + "/v1", Model: "qwen"}, Home: t.TempDir()}
		s, err := op.Run(context.Background(), skillFor(t, Trigger{}, "versionen"), Trigger{Kind: "slot"})
		if err != nil || s.Outcome != "green" {
			t.Fatalf("lease %s: %+v %v", c.lease, s, err)
		}
		var proposed string
		for _, call := range *calls {
			if strings.HasPrefix(call, "laib_propose") {
				proposed = call
			}
		}
		if !strings.Contains(proposed, `adguard/IMAGE=docker.io/adguard/adguardhome:latest@sha256:a2`) || !strings.Contains(proposed, c.want) {
			t.Fatalf("lease %s: want the digest and %q on the card: %s", c.lease, c.want, proposed)
		}
		if c.lease == werkstatt && *asked != 0 {
			t.Errorf("the model was asked while the graphics card is elsewhere")
		}
	}
}
