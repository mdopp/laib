package operator

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
)

// #376: the mechanics of a skill are fields, not prompt prose (CLAUDE.md
// rule 1). aktualisierung declares `updates: patch` and `limits: {propose:
// 3}`: the runner reads the updates itself, offers only the patch values,
// refuses any other proposal, caps the proposals, and puts the minor
// versions and the registry errors into the note for a human. The prompt
// keeps the judgment (the reason a patch is worth it).
func TestAktualisierungProposesOnlyPatchesAndNotesTheRest(t *testing.T) {
	updates := `[{"service":"immich","key":"IMAGE","image":"ghcr.io/immich-app/immich-server","current":"v1.2.0","patch":"v1.2.3","minor":"v1.4.0"},` +
		`{"service":"paperless","key":"IMAGE","image":"ghcr.io/paperless-ngx/paperless-ngx","current":"2.1.0","minor":"2.3.0"},` +
		`{"service":"radicale","key":"IMAGE","image":"docker.io/tomsquest/docker-radicale","current":"3.1.0","patch":"3.1.9"},` +
		`{"service":"mosquitto","key":"IMAGE","image":"docker.io/library/eclipse-mosquitto","current":"2.0.1","patch":"2.0.9"},` +
		`{"service":"esphome","key":"IMAGE","image":"ghcr.io/esphome/esphome","current":"1.0.0","patch":"1.0.2"},` +
		`{"service":"vaultwarden","key":"IMAGE","image":"docker.io/vaultwarden/server","current":"1.30.0","error":"registry: 401"}]`
	mcp, calls := fakeMCP(t, map[string]string{"laib_updates": updates})
	prop := func(id, svc, value string) Message {
		return Message{Role: "assistant", ToolCalls: []ToolCall{tcall(id, "laib_propose", `{"service":"`+svc+`","key":"IMAGE","value":"`+value+`","reason":"Sicherheitskorrekturen"}`)}}
	}
	llm := fakeLLM(t, []Message{
		prop("1", "immich", "ghcr.io/immich-app/immich-server:v1.4.0"),      // the minor: refused
		prop("2", "paperless", "ghcr.io/paperless-ngx/paperless-ngx:2.3.0"), // minor only: refused
		prop("3", "immich", "ghcr.io/immich-app/immich-server:v1.2.3"),      // the patch
		prop("4", "radicale", "docker.io/tomsquest/docker-radicale:3.1.9"),  // the patch
		prop("5", "mosquitto", "docker.io/library/eclipse-mosquitto:2.0.9"), // the patch
		prop("6", "esphome", "ghcr.io/esphome/esphome:1.0.2"),               // a fourth: over the cap
		{Role: "assistant", ToolCalls: []ToolCall{tcall("7", "laib_note", `{"kind":"watch","text":"Drei Patch-Vorschläge."}`)}},
		{Role: "assistant", Content: "fertig"},
	})
	var firstUser string
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir(), Typed: true}
	op.LLM.Client = &http.Client{Transport: peek(func(body string) {
		if firstUser == "" {
			firstUser = body
		}
	})}
	sk := skillFor(t, Trigger{}, "aktualisierung")
	s, err := op.Run(context.Background(), sk, Trigger{Kind: "tick"})
	if err != nil {
		t.Fatal(err)
	}
	var proposed, notes []string
	updatesFirst := false
	for i, c := range *calls {
		switch {
		case strings.HasPrefix(c, "laib_updates"):
			updatesFirst = !strings.Contains(strings.Join((*calls)[:i], "\n"), "laib_propose")
		case strings.HasPrefix(c, "laib_propose"):
			proposed = append(proposed, c)
		case strings.HasPrefix(c, "laib_note"):
			notes = append(notes, c)
		}
	}
	if !updatesFirst {
		t.Errorf("the runner did not read the updates before the model: %v", *calls)
	}
	if len(proposed) != 3 || strings.Contains(strings.Join(proposed, "\n"), "v1.4.0") || strings.Contains(strings.Join(proposed, "\n"), "2.3.0") || strings.Contains(strings.Join(proposed, "\n"), "esphome") {
		t.Fatalf("want the three first patches, nothing else: %q", proposed)
	}
	if !strings.Contains(firstUser, "immich-server:v1.2.3") || strings.Contains(firstUser, "v1.4.0") {
		t.Errorf("the model is not shown exactly the patch values:\n%s", firstUser)
	}
	note := strings.Join(notes, "\n") + s.Note
	for _, want := range []string{"paperless", "2.3.0", "v1.4.0", "vaultwarden", "registry: 401"} {
		if !strings.Contains(note, want) {
			t.Errorf("the note does not carry %q for a human: %s", want, note)
		}
	}
}

// The prose is gone from the two skills whose mechanics are fields now.
func TestTheMechanicsLeftThePrompts(t *testing.T) {
	for name, words := range map[string][]string{
		"aktualisierung": {"Höchstens", "Minor-Versionen schlägst", "Fehler beim Lesen"},
		"nutzung":        {"höchstens drei", "wörtlich"},
	} {
		sk := skillFor(t, Trigger{}, name)
		for _, w := range words {
			if strings.Contains(sk.Prompt, w) {
				t.Errorf("%s: the prompt still carries %q", name, w)
			}
		}
		if b, _ := os.ReadFile("../skills/" + name + ".yml"); !strings.Contains(string(b), "\nlimits:") {
			t.Errorf("%s declares no limits", name)
		}
	}
}
