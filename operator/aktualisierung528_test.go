package operator

import (
	"context"
	"strings"
	"testing"
)

// #528: the runner itself proposes, each once while it is open, a new image
// of a service with data — pinned by the registry's digest, the review asks
// a person and laib snapshots first — and the value a status file offers
// (Home Assistant's extensions with their release notes, HA_INSTALL). An
// image the nightly run takes (auto) is no proposal; firmware is only in
// the note.
func TestAktualisierungProposesDataImagesAndExtensionsItself(t *testing.T) {
	updates := `[{"service":"home-assistant","key":"HA_IMAGE","image":"ghcr.io/home-assistant/home-assistant","current":"stable","digest":"sha256:ha2"},` +
		`{"service":"frage","key":"FRAGE_IMAGE","image":"ghcr.io/mdopp/laib-frage","current":"latest","digest":"sha256:fr2","auto":true},` +
		`{"service":"vaultwarden","key":"IMAGE","image":"docker.io/vaultwarden/server","current":"latest","digest":"sha256:vw2"}]`
	diag := `[{"severity":"info","probe":"status","service":"home-assistant","detail":"Erweiterungen installieren: SENEC.Home 2026.10.0 · https://x/senec/releases","fix":"laib propose --set services/home-assistant/values.yml:HA_INSTALL=update.senec=2026.10.0"},` +
		`{"severity":"info","probe":"status","service":"home-assistant","detail":"Firmware: Steckdose 9.5.52 → 9.5.60 (nur gemeldet)","fix":"einspielen nur auf ein ausdrückliches Ja"},` +
		`{"severity":"info","probe":"status","service":"media","detail":"fremd","fix":"laib propose --set services/home-assistant/values.yml:HA_INSTALL=x"}]`
	inbox := `[{"id":3,"reason":"vaultwarden: neues Image docker.io/vaultwarden/server:latest (sha256:vw2)"}]`
	mcp, calls := fakeMCP(t, map[string]string{"laib_updates": updates, "laib_diag": diag, "laib_inbox": inbox})
	llm := fakeLLM(t, []Message{{Role: "assistant", Content: "fertig"}})
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir(), Typed: true}
	s, err := op.Run(context.Background(), skillFor(t, Trigger{}, "aktualisierung"), Trigger{Kind: "tick"})
	if err != nil {
		t.Fatal(err)
	}
	var proposed []string
	for _, c := range *calls {
		if strings.HasPrefix(c, "laib_propose") {
			proposed = append(proposed, c)
		}
	}
	all := strings.Join(proposed, "\n")
	if len(proposed) != 2 || !strings.Contains(all, `"value":"ghcr.io/home-assistant/home-assistant:stable@sha256:ha2"`) || !strings.Contains(all, `"key":"HA_INSTALL"`) ||
		!strings.Contains(all, `"value":"update.senec=2026.10.0"`) || !strings.Contains(all, "https://x/senec/releases") {
		t.Fatalf("want HA's image by digest and the extensions with their notes, nothing else: %q", proposed)
	}
	if strings.Contains(all, "laib-frage") || strings.Contains(all, "vaultwarden") || strings.Contains(all, `"value":"x"`) {
		t.Fatalf("the night's image, an open proposal or another service's offer was proposed: %q", proposed)
	}
	for _, want := range []string{"Firmware: Steckdose", "noch offen", "vorgeschlagen, für einen Menschen"} {
		if !strings.Contains(s.Note, want) {
			t.Errorf("the note lacks %q: %s", want, s.Note)
		}
	}
}

// #547: laib updates carries a laib row (Laib's own update, self-update.sh's):
// no proposal, and its detail is no registry error in the note.
func TestAktualisierungLeavesLaibsOwnRowAlone(t *testing.T) {
	updates := `[{"service":"laib","image":"laib","current":"v0.11.1","latest":"v0.16.0","error":"v0.16.0 zurückgenommen: nach dem Update nicht grün","auto":true}]`
	mcp, calls := fakeMCP(t, map[string]string{"laib_updates": updates, "laib_diag": `[]`, "laib_inbox": `[]`})
	llm := fakeLLM(t, []Message{{Role: "assistant", Content: "fertig"}})
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir(), Typed: true}
	s, err := op.Run(context.Background(), skillFor(t, Trigger{}, "aktualisierung"), Trigger{Kind: "tick"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if strings.HasPrefix(c, "laib_propose") {
			t.Fatalf("Laib's own row became a proposal: %s", c)
		}
	}
	if strings.Contains(s.Note, "Registry nicht lesbar") {
		t.Fatalf("the laib row's detail reads as a registry error: %s", s.Note)
	}
}
