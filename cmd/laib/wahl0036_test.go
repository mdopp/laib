package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/aufgaben"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
)

// ADR 0036 (owner, 2026-11-21): one card for one problem, its ways each a
// proposal; choosing one closes the rest.

// wahlBox files two proposals the Operator then offers as ways, with „nachdenken“
// beside them unless plain; laib adds „Als Aufgabe weitergeben“. Through the MCP verb, as ops.
func wahlBox(t *testing.T, plain bool) (env *cliEnv, holder, a, b *proposal.Proposal, out string) {
	t.Helper()
	env, _ = testBox(t)
	st, _ := cli.LoadState(env)
	a, b = proposeService(t, env, "w1"), proposeService(t, env, "w2")
	ways := []any{"Alt behalten|Die alte Fassung bleibt, nichts geht verloren.|" + strconv.Itoa(a.ID), "Neu aufspielen|Die neue Fassung kommt, die Daten bleiben.|" + strconv.Itoa(b.ID)}
	if !plain {
		ways = append(ways, "Noch einmal nachsehen|Der Operator sucht gründlicher und legt eine neue Karte an.|nachdenken")
	}
	out, code := mcp.Call(context.Background(), env, st, "ops", "laib_wahl", map[string]any{"action": "anlegen", "problem": "Vault startet nach dem Update nicht mehr.", "empfehlung": "Neu aufspielen: die Daten sind unverändert.", "wege": ways})
	if code != cli.ExitOK {
		t.Fatalf("the Operator's laib_wahl: exit %d: %s", code, out)
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	for _, p := range open {
		if len(p.Wahl) > 0 {
			holder = p
		}
	}
	if holder == nil {
		t.Fatalf("no card with ways was filed: %s", out)
	}
	return env, holder, a, b, out
}

func human(env *cliEnv) *cliEnv {
	return &cliEnv{Paths: env.Paths, Actor: "human:test", Out: io.Discard, ErrOut: io.Discard}
}

// Ablauf L-63
func TestAChoiceIsOneCardWithAButtonPerWayAndNoFreigebenOfItsOwn(t *testing.T) {
	env, h, a, b, out := wahlBox(t, false)
	if !strings.Contains(out, fmt.Sprintf("Karte #%d", h.ID)) {
		t.Fatalf("the answer does not name the card (the Operator's record reads it): %s", out)
	}
	if len(h.Wahl) != 4 || h.Ursache != "nicht gemessen" || h.Does != "Vault startet nach dem Update nicht mehr." || h.Actor != "laib" || h.Class != "human" {
		t.Fatalf("the card: %+v", h)
	}
	if h.Wahl[0].Vorschlag != a.ID || h.Wahl[1].Vorschlag != b.ID || h.Wahl[3].Vorschlag != h.ID || h.Wahl[2].Vorschlag <= h.ID {
		t.Fatalf("the ways are not the two proposals, a new repair for nachdenken and the card itself for ticket: %+v", h.Wahl)
	}
	think, _ := proposal.Get(cli.InboxDir(env), h.Wahl[2].Vorschlag)
	if diff := proposal.Diff(context.Background(), env.Paths.State, think); !strings.Contains(diff, fmt.Sprintf("laib wahl nachdenken %d", h.ID)) || think.Actor != "laib" {
		t.Fatalf("nachdenken is not a repair of the card: %s", diff)
	}
	body := inboxMux(env)("GET", "/inbox", nil)
	for _, w := range []string{h.Does, "nicht gemessen", "Empfehlung: Neu aufspielen", "Neu aufspielen: die Daten sind unverändert.", "Alt behalten", "Die alte Fassung bleibt, nichts geht verloren.", "Noch einmal nachsehen", "Als Aufgabe weitergeben",
		fmt.Sprintf("/inbox/%d/approve", a.ID), fmt.Sprintf("/inbox/%d/approve", b.ID), fmt.Sprintf("/inbox/%d/approve", think.ID), fmt.Sprintf("/inbox/%d/approve", h.ID), fmt.Sprintf("/inbox/%d/reject", h.ID)} {
		if !strings.Contains(body, w) {
			t.Errorf("the card lacks %q:\n%s", w, body)
		}
	}
	if strings.Contains(body, ">Freigeben<") {
		t.Errorf("the card has a Freigeben of its own beside its ways:\n%s", body)
	}
	for _, q := range []*proposal.Proposal{a, b, think} {
		if strings.Contains(body, fmt.Sprintf("<section class=\"card\" id=%d", q.ID)) || strings.Contains(body, fmt.Sprintf("id=%d data-f", q.ID)) {
			t.Errorf("way #%d shows as a card of its own:\n%s", q.ID, body)
		}
	}
}

// Ablauf L-64
func TestChoosingAWayRejectsTheOthersAndClosesTheCard(t *testing.T) {
	env, h, a, b, _ := wahlBox(t, false)
	if code := inbox.Decide(context.Background(), human(env), []string{strconv.Itoa(a.ID)}, true); code != cli.ExitOK {
		t.Fatalf("approving the way: exit %d", code)
	}
	get := func(id int) *proposal.Proposal { p, _ := proposal.Get(cli.InboxDir(env), id); return p }
	if get(a.ID).Status != "approved" {
		t.Fatalf("the chosen way: %+v", get(a.ID))
	}
	for _, id := range []int{b.ID, h.Wahl[2].Vorschlag} {
		if q := get(id); q.Status != "rejected" || q.RejectReason != "anderer Weg gewählt: Alt behalten" {
			t.Errorf("#%d stays open or says the wrong thing: %+v", id, q)
		}
	}
	if q := get(h.ID); q.Status != "rejected" || q.RejectReason != "Weg gewählt: Alt behalten" {
		t.Errorf("the card is not closed by the choice: %+v", q)
	}
	if open, _ := proposal.List(cli.InboxDir(env), true); len(open) != 0 {
		t.Errorf("something stays open: %+v", open)
	}
	if body := inboxMux(env)("GET", "/inbox", nil); !strings.Contains(body, "Weg gewählt: Alt behalten") || strings.Contains(body, "anderer Weg gewählt") {
		t.Errorf("the Inbox shows the closed card wrongly:\n%s", body)
	}
	if code := inbox.Decide(context.Background(), human(env), []string{strconv.Itoa(b.ID)}, true); code == cli.ExitOK {
		t.Fatal("a second way was approved")
	}
}

// Ablauf L-64
func TestRejectingTheCardRejectsEveryWay(t *testing.T) {
	env, h, a, b, _ := wahlBox(t, false)
	if code := inbox.Decide(context.Background(), human(env), []string{strconv.Itoa(h.ID), "zu", "teuer"}, false); code != cli.ExitOK {
		t.Fatalf("rejecting the card: exit %d", code)
	}
	if open, _ := proposal.List(cli.InboxDir(env), true); len(open) != 0 {
		t.Fatalf("a way stays open: %+v", open)
	}
	if q, _ := proposal.Get(cli.InboxDir(env), a.ID); q.Status != "rejected" || !strings.Contains(q.RejectReason, "kein Weg gewählt") {
		t.Fatalf("%+v", q)
	}
	if q, _ := proposal.Get(cli.InboxDir(env), b.ID); q.Status != "rejected" {
		t.Fatalf("%+v", q)
	}
}

// Ablauf L-64
func TestTheTicketWayMakesABoxAufgabeAndNachdenkenOpensAnIncidentForTheOperator(t *testing.T) {
	env, h, a, _, _ := wahlBox(t, false)
	since := time.Now().Add(-time.Second)
	if code := inbox.Decide(context.Background(), human(env), []string{strconv.Itoa(h.Wahl[2].Vorschlag)}, true); code != cli.ExitOK {
		t.Fatalf("nachdenken: exit %d", code)
	}
	es, _, _ := journal.ReadCounted(env.Paths.Journal, since)
	if !slices.ContainsFunc(es, func(e journal.Entry) bool {
		return e.Verb == "wahl" && e.Result == "red" && e.Actor == "human:test" && strings.HasPrefix(e.Detail, "nachdenken: ") && strings.Contains(e.Detail, "Vault startet nach dem Update nicht mehr.") && strings.Contains(e.Detail, "Alt behalten")
	}) {
		t.Fatalf("no red line sends the card back to the Operator: %+v", es)
	}
	if q, _ := proposal.Get(cli.InboxDir(env), a.ID); q.Status != "rejected" {
		t.Fatalf("thinking again left the other ways open: %+v", q)
	}
	// ticket, on a second card
	env, h, _, _, _ = wahlBox(t, false)
	if code := inbox.Decide(context.Background(), human(env), []string{strconv.Itoa(h.ID)}, true); code != cli.ExitOK {
		t.Fatalf("ticket: exit %d", code)
	}
	list, err := aufgaben.Load(env.Paths.State)
	if err != nil || len(list) != 1 || list[0].Titel != "Vault startet nach dem Update nicht mehr." || !strings.Contains(list[0].Warum, "Alt behalten: Die alte Fassung bleibt") || list[0].Fuer != aufgaben.Admins || list[0].Weg == nil || !list[0].Weg.Laib {
		t.Fatalf("the task for the admins: %+v %v", list, err)
	}
	if open, _ := proposal.List(cli.InboxDir(env), true); len(open) != 0 {
		t.Fatalf("something stays open after the ticket: %+v", open)
	}
}

func TestOnlyTheTwoWahlFormsWithANumberAreRepairs(t *testing.T) {
	env, _ := testBox(t)
	st, _ := cli.LoadState(env)
	for line, want := range map[string]bool{
		"wahl nachdenken 12": true, "wahl ticket 7": true,
		"wahl anlegen": false, "wahl ticket": false, "wahl ticket x": false, "wahl nachdenken 1 2": false, "wahl ticket -1": false, "wahl ticket 1;ls": false, "wahl": false, "wahl warten 3": false,
	} {
		if got := inbox.Repairable(st, strings.Fields(line)); got != want {
			t.Errorf("Repairable(%q) = %v, want %v", line, got, want)
		}
	}
	for _, hand := range []string{"ops:x", "claude", "bote"} { // an agent does not send a card back or file its task; a human's approval does
		var errs strings.Builder
		if code := inbox.Wahl(context.Background(), &cliEnv{Paths: env.Paths, Actor: hand, Out: io.Discard, ErrOut: &errs}, []string{"ticket", "1"}); code == cli.ExitOK {
			t.Errorf("%s ran a way", hand)
		}
	}
}

func TestACardNeedsOneToThreeWaysOfOpenProposalsAndWordsForPeople(t *testing.T) {
	env, _ := testBox(t)
	st, _ := cli.LoadState(env)
	a := proposeService(t, env, "w1")
	n := strconv.Itoa(a.ID)
	call := func(problem string, ways ...string) int {
		raw := make([]any, len(ways))
		for i, w := range ways {
			raw[i] = w
		}
		_, code := mcp.Call(context.Background(), env, st, "ops", "laib_wahl", map[string]any{"action": "anlegen", "problem": problem, "empfehlung": "Der erste.", "wege": raw})
		return code
	}
	for what, code := range map[string]int{
		"no way":            call("Etwas ist los."),
		"a closed proposal": call("Etwas ist los.", "Eins|Das tut etwas.|"+n, "Zwei|Das auch.|999"),
		"ticket by hand":    call("Etwas ist los.", "Eins|Das tut etwas.|"+n, "Zwei|Das auch.|ticket"),
		"nachdenken twice":  call("Etwas ist los.", "Eins|Das tut etwas.|nachdenken", "Zwei|Das auch.|nachdenken"),
		"four ways":         call("Etwas ist los.", "A|a.|nachdenken", "B|b.|"+n, "C|c.|"+n, "D|d.|"+n),
		"a command":         call("Starte laib restart vault neu.", "Eins|Das tut etwas.|"+n, "Zwei|Das auch.|nachdenken"),
		"a technical way":   call("Etwas ist los.", "Eins|exit status 3, siehe vault.service|"+n, "Zwei|Das auch.|nachdenken"),
		"no folge":          call("Etwas ist los.", "Eins||"+n, "Zwei|Das auch.|nachdenken"),
		"a long name":       call("Etwas ist los.", strings.Repeat("a", 41)+"|Das tut etwas.|"+n, "Zwei|Das auch.|nachdenken"),
	} {
		if code == cli.ExitOK {
			t.Errorf("%s was filed", what)
		}
	}
	if open, _ := proposal.List(cli.InboxDir(env), true); len(open) != 1 {
		t.Fatalf("a refused card left proposals behind: %+v", open)
	}
	if _, code := mcp.Call(context.Background(), env, st, "ops", "laib_wahl", map[string]any{"action": "nachdenken", "problem": "x", "empfehlung": "y", "wege": []any{"a|b|1"}}); code == cli.ExitOK {
		t.Error("the Operator may call a way directly")
	}
	if _, code := mcp.Call(context.Background(), env, st, "resident", "laib_wahl", map[string]any{"action": "anlegen", "problem": "Etwas ist los.", "empfehlung": "Der erste.", "wege": []any{"Eins|Das tut etwas.|" + n, "Zwei|Das auch.|nachdenken"}}); code == cli.ExitOK {
		t.Error("a hand that may not propose filed a card")
	}
}
