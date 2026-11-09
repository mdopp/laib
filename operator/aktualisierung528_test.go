package operator

import (
	"context"
	"strings"
	"testing"
)

// #528: the runner itself proposes, each once while it is open, the value a
// status file offers (Home Assistant's extensions with their release notes,
// HA_INSTALL); device firmware HA can install is its own value, HA_FIRMWARE;
// other firmware is only in the note. A new digest is no longer its own:
// the nightly versionen proposes every one (ADR 0034).
func TestAktualisierungProposesDataImagesAndExtensionsItself(t *testing.T) {
	updates := `[{"service":"home-assistant","key":"HA_IMAGE","image":"ghcr.io/home-assistant/home-assistant","current":"stable","digest":"sha256:ha2"},` +
		`{"service":"frage","key":"FRAGE_IMAGE","image":"ghcr.io/mdopp/laib-frage","current":"latest","digest":"sha256:fr2","auto":true},` +
		`{"service":"vaultwarden","key":"IMAGE","image":"docker.io/vaultwarden/server","current":"latest","digest":"sha256:vw2"}]`
	diag := `[{"severity":"info","probe":"status","service":"home-assistant","detail":"Erweiterungen installieren: SENEC.Home 2026.10.0 · https://x/senec/releases","fix":"laib propose --set services/home-assistant/values.yml:HA_INSTALL=update.senec=2026.10.0"},` +
		`{"severity":"info","probe":"status","service":"home-assistant","detail":"Firmware: Steckdose 9.5.52 → 9.5.60 (nur gemeldet)","fix":"mit der App des Herstellers"},` +
		`{"severity":"info","probe":"status","service":"home-assistant","detail":"Firmware: Lampe 1.2 → 1.3","fix":"der Operator schlägt es samstags vor (HA_FIRMWARE)"},` +
		`{"severity":"info","probe":"status","service":"home-assistant","detail":"Neue Firmware für Geräte einspielen: Lampe 1.2 → 1.3","fix":"laib propose --set services/home-assistant/values.yml:HA_FIRMWARE=update.lampe=1.3"},` +
		`{"severity":"info","probe":"status","service":"media","detail":"fremd","fix":"laib propose --set services/home-assistant/values.yml:HA_INSTALL=x"}]`
	inbox := `[{"id":3,"reason":"vaultwarden: neues Image docker.io/vaultwarden/server:latest (sha256:vw2)"}]`
	mcp, calls := fakeMCP(t, map[string]string{"laib_updates": updates, "laib_diag": diag, "laib_inbox": inbox})
	llm := fakeLLM(t, []Message{{Role: "assistant", Content: "fertig"}})
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir(), Typed: true}
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
	if len(proposed) != 2 || !strings.Contains(all, `"key":"HA_FIRMWARE"`) || !strings.Contains(all, `"value":"update.lampe=1.3"`) || !strings.Contains(all, `"key":"HA_INSTALL"`) ||
		!strings.Contains(all, `"value":"update.senec=2026.10.0"`) || !strings.Contains(all, "https://x/senec/releases") {
		t.Fatalf("want the firmware and the extensions with their notes, nothing else: %q", proposed)
	}
	if strings.Contains(all, "@sha256:") || strings.Contains(all, `"value":"x"`) {
		t.Fatalf("a digest (versionen's, ADR 0034) or another service's offer was proposed: %q", proposed)
	}
	if strings.Contains(s.Note, "Firmware: Lampe") {
		t.Errorf("firmware HA installs is a proposal, not a line for a human: %s", s.Note)
	}
	for _, want := range []string{"Firmware: Steckdose", "vorgeschlagen, für einen Menschen"} {
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
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir(), Typed: true}
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
