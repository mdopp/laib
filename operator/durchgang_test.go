package operator

import (
	"context"
	"strings"
	"testing"
)

// Durchgang rot (owner, 2026-11-14): the red apply event runs the skill's
// script through the real door (skills/laibskill.py); it asks the model one
// question, proposes the innocent approval again and puts the cause on the
// run's cards with its note. It calls no verb that approves or rolls back.
// Ablauf L-50
func TestARedRunIsReadAndItsInnocentApprovalProposedAgain(t *testing.T) {
	run := "#542, #543 (f548fec..7fc9485)"
	journal := `[{"t":"2026-11-14T09:55:20Z","verb":"apply","result":"red","reason":"Freigaben ` + run + `","detail":"home-assistant: post-deploy exit status 1\nhome-assistant: update.install update.meross_lan_update v6.0.1: HTTP 500"},` +
		`{"t":"2026-11-14T09:57:01Z","verb":"apply","result":"green","reason":"rollback ` + run + `"}]`
	inbox := `[{"id":542,"status":"approved","outcome":"rolled-back","batch":"` + run + `","reason":"Neue Versionen: beets","files":["services/beets/values.yml"],"intent":{"set":{"services/beets/values.yml:IMAGE":"docker.io/linuxserver/beets@sha256:ee3c"}}},` +
		`{"id":543,"status":"approved","outcome":"rolled-back","batch":"` + run + `","reason":"home-assistant: Erweiterungen installieren","files":["services/home-assistant/values.yml"],"restore":547,"intent":{"set":{"services/home-assistant/values.yml:HA_INSTALL":"update.meross_lan_update=v6.0.1"}}}]`
	mcp, calls := fakeMCP(t, map[string]string{"laib_journal": journal, "laib_inbox": inbox, "laib_logs": `{"home-assistant.service":""}`, "laib_propose": `{"id": 571}`})
	lease, catalogue := household, loaded
	llm, asked := fakeLease(t, &lease, &catalogue, []Message{{Role: "assistant", Content: "Ursache: Home Assistant hat die Erweiterungen abgelehnt.\nBesitzer: nein"}})
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL + "/v1", Model: "qwen"}, Home: t.TempDir(), Typed: true}
	r := &Runner{Op: op}
	ss := r.Event(context.Background(), []Skill{skillFor(t, Trigger{}, "durchgang")}, Trigger{Kind: "event", Detail: ": apply: home-assistant: post-deploy exit status 1"})
	if len(ss) != 1 || ss[0].Outcome != "green" || *asked != 1 {
		t.Fatalf("one session, green, after one question to the model: %+v", ss)
	}
	all := strings.Join(*calls, "\n")
	for _, want := range []string{`laib_propose {"reason":"Neue Versionen: beets (neu vorgeschlagen: #542 wurde mit dem roten Durchgang von #543 zurückgenommen)","sets":["beets/IMAGE=docker.io/linuxserver/beets@sha256:ee3c"]}`,
		`"karten":"542,543,547"`, "Home Assistant hat die Erweiterungen abgelehnt", "#542 als #571"} {
		if !strings.Contains(all, want) {
			t.Errorf("the session lacks %q:\n%s", want, all)
		}
	}
	for _, never := range []string{"laib_approve", "laib_rollback", "laib_apply", `"sets":["home-assistant/`} {
		if strings.Contains(all, never) {
			t.Errorf("the session called %q or proposed the red approval again:\n%s", never, all)
		}
	}
}
