package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/envelope"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
)

// #828: Haus records the family saying the wake word; once enough new ones
// are there, Stimme's status offers „neu trainieren“. No retrain without the
// owner: the repair is `laib retrain stimme`, a destroy only a human
// approves; approved, laib starts stimme's retrain unit, which moves the
// model aside (the copy stays) so the trainer trains again.
const retrainFinding = `{"at":"%s","detail":"Wake-Word „Hey Joshua“ auf dem Gerät „Voice 0907c9“","phrase":"Hey Joshua",
"findings":[{"severity":"gelb","detail":"6 neue eigene Aufnahmen von „Hey Joshua“ seit dem letzten Training","fix":"laib repair status stimme",
"repair":["retrain","stimme"],"topic":"training","does":"„Hey Joshua“ wird mit euren Aufnahmen neu trainiert",
"effect":"Laib legt das jetzige Modell beiseite und trainiert „Hey Joshua“ neu; das dauert einige Stunden.",
"undo":"Das bisherige Modell bleibt als Kopie liegen (stimme/micro/vorher)."}]}`

func TestTheWakeWordIsTrainedAgainOnlyAfterTheOwnerApproves(t *testing.T) {
	ctx := context.Background()
	env, calls := flashBox(t)
	dir := filepath.Join(env.Paths.State, "services", "stimme")
	if err := os.WriteFile(filepath.Join(dir, "stimme-retrain.service"), []byte("[Service]\nType=oneshot\nExecStart=/bin/true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := proposal.CommitDirty(ctx, env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	st, _ := cli.LoadState(env)
	status := filepath.Join(st.Box.DataDir, "stimme", "micro", "laib-status.json")
	if err := os.WriteFile(status, []byte(strings.Replace(retrainFinding, "%s", time.Now().UTC().Format(time.RFC3339), 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	retrained := func() bool {
		return slices.ContainsFunc(calls(), func(c string) bool { return strings.Contains(c, "start --no-block stimme-retrain.service") })
	}
	if !inbox.Repairable(st, []string{"retrain", "stimme"}) || inbox.Repairable(st, []string{"retrain", "esphome"}) || inbox.Repairable(st, []string{"retrain", "whoami"}) {
		t.Fatal("only a service with a retrain unit retrains")
	}
	if _, code := mcp.Call(ctx, env, st, "ops", "laib_retrain", map[string]any{"args": "stimme"}); code == cli.ExitOK || retrained() {
		t.Fatal("an agent retrained itself")
	}
	if code := envelope.Retrain(ctx, &cliEnv{Paths: env.Paths, Actor: "claude"}, []string{"stimme"}); code == cli.ExitOK || retrained() {
		t.Fatal("a hand, not a human, ran laib retrain")
	}
	if out, code := mcp.Call(ctx, env, st, "ops", "laib_repair", map[string]any{"probe": "status", "service": "stimme"}); code != cli.ExitOK {
		t.Fatalf("the Operator's repair proposal: exit %d: %s", code, out)
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	if len(open) != 1 || open[0].Tier != "destroy" || open[0].Class != "human" || retrained() {
		t.Fatalf("want one open destroy for a human, nothing retrained yet: %+v", open)
	}
	card := proposal.Card(open[0], "box.test")
	if card.Summary != "„Hey Joshua“ wird mit euren Aufnahmen neu trainiert" || !strings.Contains(card.Why, "neue eigene Aufnahmen") ||
		!slices.ContainsFunc(card.Impact, func(s string) bool { return strings.Contains(s, "einige Stunden") }) || !strings.Contains(card.Reversible, "stimme/micro/vorher") {
		t.Fatalf("the card must say what happens and where the old model stays: %+v", card)
	}
	id := strconv.Itoa(open[0].ID)
	if _, code := mcp.Call(ctx, env, st, "ops", "laib_approve", map[string]any{"args": id}); code != cli.ExitRefused || retrained() {
		t.Fatalf("an agent approved a retrain: exit %d", code)
	}
	if code := inbox.Decide(ctx, &cliEnv{Paths: env.Paths, Actor: "human:test"}, []string{id}, true); code != cli.ExitOK || !retrained() {
		t.Fatalf("approved by a human: exit %d, calls %v", code, calls())
	}
	es, _ := journal.Read(env.Paths.Journal, time.Time{})
	if !slices.ContainsFunc(es, func(e journal.Entry) bool {
		return e.Verb == "retrain" && e.Actor == "human:test" && e.Service == "stimme"
	}) {
		t.Fatalf("the retrain runs as the human who approved, journaled: %+v", es)
	}
}
