package inbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// ADR 0036: the choice card, in this package's own tests (the trust kernel's
// mutants are killed by the package's tests, scripts/ratchet.py mutation).

// wahlBox: the golden state as a real Git repo, the host faked, RunVerb recording what a repair runs.
func wahlBox(t *testing.T) (env *cli.Env, st *state.State, ran *[][]string, stdin *[]string) {
	t.Helper()
	real, realVerb := sysd.Run, RunVerb
	t.Cleanup(func() { sysd.Run, RunVerb = real, realVerb })
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "git" {
			return real(ctx, name, args...)
		}
		return "", nil
	}
	ran, stdin = &[][]string{}, &[]string{}
	RunVerb = func(ctx context.Context, env *cli.Env, argv []string) int {
		*ran = append(*ran, argv)
		in, _ := io.ReadAll(env.Stdin())
		*stdin = append(*stdin, string(in))
		return cli.ExitOK
	}
	home := t.TempDir()
	stDir := filepath.Join(home, "state")
	if err := os.CopyFS(stDir, os.DirFS("../../testdata/golden/basic/state")); err != nil {
		t.Fatal(err)
	}
	env = &cli.Env{Actor: "ops", Out: io.Discard, ErrOut: io.Discard, Paths: boxfs.Paths{Home: home, State: stDir, Journal: filepath.Join(home, "journal")}}
	if err := proposal.EnsureRepo(context.Background(), stDir); err != nil {
		t.Fatal(err)
	}
	var err error
	if st, err = cli.LoadState(env); err != nil {
		t.Fatal(err)
	}
	return env, st, ran, stdin
}

func as(env *cli.Env, actor string) *cli.Env {
	e := *env
	e.Actor = actor
	return &e
}

// repairs files n open repairs, the ways a card offers.
func repairs(t *testing.T, env *cli.Env, st *state.State, n int) []*proposal.Proposal {
	t.Helper()
	var out []*proposal.Proposal
	for i := range n {
		p, err := fileRepair(context.Background(), env, st, "Weg "+strconv.Itoa(i+1), "Weg "+strconv.Itoa(i+1), "nichts", nil, []string{"laib restart whoami"})
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func anlegen(env *cli.Env, problem string, ways ...string) (string, int) {
	var out bytes.Buffer
	e := *env
	e.Out, e.ErrOut = &out, &out
	args := []string{"anlegen", "--problem", problem, "--empfehlung", "Der erste Weg: er ist am sichersten.", "--"}
	code := Wahl(context.Background(), &e, append(args, ways...))
	return out.String(), code
}

func get(t *testing.T, env *cli.Env, id int) *proposal.Proposal {
	t.Helper()
	p, err := proposal.Get(cli.InboxDir(env), id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOnlyTheTwoWahlFormsWithANumberAreRepairs(t *testing.T) {
	_, st, _, _ := wahlBox(t)
	for _, c := range []struct {
		argv []string
		want bool
	}{
		{[]string{"wahl", "nachdenken", "12"}, true}, {[]string{"wahl", "ticket", "7"}, true}, {[]string{"wahl", "ticket", "0"}, true},
		{[]string{"wahl", "anlegen", "1"}, false}, {[]string{"wahl", "warten", "3"}, false}, {[]string{"wahl", "ticket"}, false}, {[]string{"wahl"}, false},
		{[]string{"wahl", "ticket", ""}, false}, {[]string{"wahl", "ticket", "x"}, false}, {[]string{"wahl", "ticket", "-1"}, false}, {[]string{"wahl", "ticket", "1;ls"}, false},
		{[]string{"wahl", "nachdenken", "1", "2"}, false}, {[]string{"wahlen", "ticket", "1"}, false},
	} {
		if got := Repairable(st, c.argv); got != c.want {
			t.Errorf("Repairable(%q) = %v, want %v", c.argv, got, c.want)
		}
	}
}

func TestACardIsFiledWithItsWaysItsTicketAndItsThinkingAgain(t *testing.T) {
	env, st, _, _ := wahlBox(t)
	w := repairs(t, env, st, 2)
	out, code := anlegen(env, "Vault startet nach dem Update nicht mehr.",
		"Alt behalten|Die alte Fassung bleibt.|"+strconv.Itoa(w[0].ID), "Neu aufspielen|Die neue Fassung kommt.|"+strconv.Itoa(w[1].ID), "Noch einmal nachsehen|Der Operator sucht gründlicher.|nachdenken")
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	h, think := get(t, env, 3), get(t, env, 4)
	if !strings.Contains(out, "Karte #3 angelegt") || !strings.Contains(out, "4 Wegen") {
		t.Errorf("the answer: %q", out)
	}
	if h.Does != "Vault startet nach dem Update nicht mehr." || h.Ursache != "nicht gemessen" || h.Empfehlung != "Der erste Weg: er ist am sichersten." || h.Class != "human" || h.Status != "open" || len(h.Reasons) != 0 {
		t.Fatalf("the card: %+v", h)
	}
	if !slices.Equal(h.Wahl, []proposal.Weg{{Label: "Alt behalten", Folge: "Die alte Fassung bleibt.", Vorschlag: 1}, {Label: "Neu aufspielen", Folge: "Die neue Fassung kommt.", Vorschlag: 2},
		{Label: "Noch einmal nachsehen", Folge: "Der Operator sucht gründlicher.", Vorschlag: 4}, {Label: "Als Aufgabe weitergeben", Folge: "Die Karte wird eine Aufgabe für die Admins; auf der Box ändert sich nichts.", Vorschlag: 3}}) {
		t.Fatalf("the ways: %+v", h.Wahl)
	}
	if !slices.Equal(h.Facts, []string{"Alt behalten: Die alte Fassung bleibt.", "Neu aufspielen: Die neue Fassung kommt.", "Noch einmal nachsehen: Der Operator sucht gründlicher.",
		"Als Aufgabe weitergeben: Die Karte wird eine Aufgabe für die Admins; auf der Box ändert sich nichts.", "Empfehlung: Der erste Weg: er ist am sichersten."}) {
		t.Errorf("the facts: %q", h.Facts)
	}
	if d := proposal.Diff(context.Background(), env.Paths.State, h); !strings.HasSuffix(strings.TrimSpace(d), "laib wahl ticket 3") {
		t.Errorf("the card does not carry its own ticket: %s", d)
	}
	if d := proposal.Diff(context.Background(), env.Paths.State, think); !strings.HasSuffix(strings.TrimSpace(d), "laib wahl nachdenken 3") || think.Actor != "laib" || think.Does != "Noch einmal gründlich nachdenken" {
		t.Errorf("nachdenken: %+v %s", think, d)
	}
	es, _ := journal.Read(env.Paths.Journal, time.Time{})
	if !slices.ContainsFunc(es, func(e journal.Entry) bool {
		return e.Verb == "wahl" && e.Result == "info" && e.Actor == "ops" && e.Detail == "#3 wartet auf die Wahl zwischen 4 Wegen"
	}) {
		t.Errorf("the journal: %+v", es)
	}
	if open, _ := proposal.List(cli.InboxDir(env), true); len(open) != 4 {
		t.Errorf("want the two ways, the card and its nachdenken open: %d", len(open))
	}
}

func TestACardWithOneOrThreeWaysAndAnUrsacheIsFiled(t *testing.T) {
	env, st, _, _ := wahlBox(t)
	w := repairs(t, env, st, 3)
	id := func(i int) string { return strconv.Itoa(w[i].ID) }
	if out, code := anlegen(env, "Eins.", strings.Repeat("ä", 40)+"|Nur einer.|"+id(0)); code != cli.ExitOK { // one way, a name of 40 letters
		t.Fatalf("one way and a name of 40 letters: %d %s", code, out)
	}
	var out bytes.Buffer
	e := *env
	e.Out, e.ErrOut = &out, &out
	if code := Wahl(context.Background(), &e, []string{"anlegen", "--problem", "Drei.", "--ursache", "Die Platte ist voll.", "--empfehlung", "Der erste.", "--", "A|a.|" + id(0), "B|b.|" + id(1), "C|c.|" + id(2)}); code != cli.ExitOK {
		t.Fatalf("three ways: %d %s", code, out.String())
	}
	if h := get(t, env, 6); h.Ursache != "Die Platte ist voll." || len(h.Wahl) != 4 {
		t.Fatalf("%+v", h)
	}
	if h := get(t, env, 4); len(h.Wahl) != 2 || h.Wahl[1].Vorschlag != 4 {
		t.Fatalf("the ticket is the card itself: %+v", h.Wahl)
	}
}

func TestACardIsRefusedAndLeavesNothingBehind(t *testing.T) {
	env, st, _, _ := wahlBox(t)
	w := repairs(t, env, st, 4)
	n := strconv.Itoa(w[0].ID)
	closed := repairs(t, env, st, 1)[0]
	closed.Status = "approved"
	proposal.Save(cli.InboxDir(env), closed)
	cases := map[string][]string{
		"no way":            {},
		"four ways":         {"A|a.|nachdenken", "B|b.|" + strconv.Itoa(w[1].ID), "C|c.|" + strconv.Itoa(w[2].ID), "D|d.|" + strconv.Itoa(w[3].ID)},
		"a closed one":      {"A|a.|" + n, "B|b.|" + strconv.Itoa(closed.ID)},
		"an unknown one":    {"A|a.|" + n, "B|b.|99"},
		"nachdenken twice":  {"A|a.|nachdenken", "B|b.|nachdenken"},
		"ticket by hand":    {"A|a.|" + n, "B|b.|ticket"},
		"no folge":          {"A||" + n},
		"a blank name":      {" |a.|" + n},
		"a long name":       {strings.Repeat("a", 41) + "|a.|" + n},
		"a command":         {"A|Dann läuft laib restart vault.|" + n},
		"a command in name": {"laib restart|a.|" + n},
		"a unit":            {"A|vault.service startet.|" + n},
		"two fields":        {"A|a."},
	}
	for what, ways := range cases {
		if out, code := anlegen(env, "Etwas ist los.", ways...); code == cli.ExitOK {
			t.Errorf("%s was filed: %s", what, out)
		}
	}
	for what, args := range map[string][]string{
		"no problem":          {"anlegen", "--empfehlung", "x", "--", "A|a.|" + n},
		"no empfehlung":       {"anlegen", "--problem", "x", "--", "A|a.|" + n},
		"a technical one":     {"anlegen", "--problem", "Starte laib restart vault.", "--empfehlung", "x", "--", "A|a.|" + n},
		"a technical ursache": {"anlegen", "--problem", "x", "--ursache", "exit status 3", "--empfehlung", "x", "--", "A|a.|" + n},
		"a dangling flag":     {"anlegen", "--problem", "x", "--empfehlung", "x", "--ursache", "--", "A|a.|" + n},
		"nothing":             {},
		"another verb":        {"loeschen", "1"},
	} {
		var out bytes.Buffer
		e := *env
		e.Out, e.ErrOut = &out, &out
		if code := Wahl(context.Background(), &e, args); code == cli.ExitOK {
			t.Errorf("%s was filed", what)
		}
		if what == "no problem" && !strings.Contains(out.String(), "die Felder für Menschen") {
			t.Errorf("a card with ways after a leading -- is read wrongly: %q", out.String())
		}
	}
	if code := Wahl(context.Background(), as(env, "resident"), []string{"anlegen", "--problem", "x", "--empfehlung", "x", "--", "A|a.|" + n}); code == cli.ExitOK {
		t.Error("a hand that may not propose filed a card")
	}
	if open, _ := proposal.List(cli.InboxDir(env), true); len(open) != 4 {
		t.Errorf("a refused card left proposals behind: %d open, want the four ways", len(open))
	}
}

func TestOnlyAHumanRunsAWayAndItNeedsACardWithWays(t *testing.T) {
	env, st, ran, _ := wahlBox(t)
	w := repairs(t, env, st, 2)
	anlegen(env, "Etwas ist los.", "A|a.|"+strconv.Itoa(w[0].ID), "B|b.|"+strconv.Itoa(w[1].ID))
	for _, actor := range []string{"ops", "claude", "bote", "laib"} {
		if code := Wahl(context.Background(), as(env, actor), []string{"ticket", "3"}); code == cli.ExitOK {
			t.Errorf("%s ran a way", actor)
		}
	}
	human := as(env, "human:test")
	for what, args := range map[string][]string{"an unknown card": {"ticket", "99"}, "a proposal without ways": {"ticket", "1"}, "no number": {"ticket", "x"}, "too many": {"ticket", "3", "4"}, "another way": {"warten", "3"}} {
		if code := Wahl(context.Background(), human, args); code == cli.ExitOK {
			t.Errorf("%s ran", what)
		}
	}
	if len(*ran) != 0 {
		t.Fatalf("a refused way ran %v", *ran)
	}
	if code := Wahl(context.Background(), human, []string{"nachdenken", "1"}); code == cli.ExitOK {
		t.Error("nachdenken ran for a proposal without ways")
	}
}

func TestTheTicketIsABoxAufgabeForTheAdminsWithTheCardsWords(t *testing.T) {
	env, st, ran, stdin := wahlBox(t)
	w := repairs(t, env, st, 2)
	anlegen(env, "Vault startet nicht.", "Alt behalten|Die alte Fassung bleibt.|"+strconv.Itoa(w[0].ID), "Neu aufspielen|Die neue Fassung kommt.|"+strconv.Itoa(w[1].ID))
	var out bytes.Buffer
	human := as(env, "human:test")
	human.Out = &out
	if code := Wahl(context.Background(), human, []string{"ticket", "3"}); code != cli.ExitOK || !strings.Contains(out.String(), "Box-Aufgabe") {
		t.Fatalf("%d %q", code, out.String())
	}
	if len(*ran) != 1 || !slices.Equal((*ran)[0], []string{"aufgabe_kern", "add"}) {
		t.Fatalf("ran %v", *ran)
	}
	var task struct {
		Titel, Warum string
		Weg          struct{ Laib bool }
	}
	if err := json.Unmarshal([]byte((*stdin)[0]), &task); err != nil || task.Titel != "Vault startet nicht." || !task.Weg.Laib ||
		!strings.Contains(task.Warum, "Ursache: nicht gemessen.") || !strings.Contains(task.Warum, "Alt behalten: Die alte Fassung bleibt. · Neu aufspielen: Die neue Fassung kommt.") || !strings.Contains(task.Warum, "Empfehlung: Der erste Weg") {
		t.Fatalf("the task: %q %v", *stdin, err)
	}
	RunVerb = func(context.Context, *cli.Env, []string) int { return cli.ExitRed }
	if code := Wahl(context.Background(), human, []string{"ticket", "3"}); code != cli.ExitRed {
		t.Errorf("a task that was not made ended %d", code)
	}
}

func TestNachdenkenSendsTheProblemBackAsARedLineTheOperatorOpensAnIncidentOn(t *testing.T) {
	env, st, ran, _ := wahlBox(t)
	w := repairs(t, env, st, 2)
	anlegen(env, "Vault startet nicht.", "Alt behalten|Die alte Fassung bleibt.|"+strconv.Itoa(w[0].ID), "Neu aufspielen|Die neue Fassung kommt.|"+strconv.Itoa(w[1].ID))
	if code := Wahl(context.Background(), as(env, "human:test"), []string{"nachdenken", "3"}); code != cli.ExitOK {
		t.Fatalf("exit %d", code)
	}
	es, _ := journal.Read(env.Paths.Journal, time.Time{})
	i := slices.IndexFunc(es, func(e journal.Entry) bool { return e.Verb == "wahl" && e.Result == "red" })
	if i < 0 || es[i].Actor != "human:test" || !strings.HasPrefix(es[i].Detail, "nachdenken: Karte #3 zurückgegeben") ||
		!strings.Contains(es[i].Detail, "Vault startet nicht. Ursache: nicht gemessen.") || !strings.Contains(es[i].Detail, "Alt behalten: Die alte Fassung bleibt.") {
		t.Fatalf("no red line sends the card back: %+v", es)
	}
	if len(*ran) != 0 {
		t.Errorf("nachdenken ran a verb: %v", *ran)
	}
}

// chosen files a card of two repairs and a nachdenken, and returns the card, the ways and nachdenken.
func chosen(t *testing.T) (env *cli.Env, h, a, b, think *proposal.Proposal) {
	t.Helper()
	env, st, _, _ := wahlBox(t)
	w := repairs(t, env, st, 2)
	if out, code := anlegen(env, "Vault startet nicht.", "Alt behalten|Die alte Fassung bleibt.|"+strconv.Itoa(w[0].ID), "Neu aufspielen|Die neue Fassung kommt.|"+strconv.Itoa(w[1].ID), "Noch einmal nachsehen|Gründlicher.|nachdenken"); code != cli.ExitOK {
		t.Fatalf("%d %s", code, out)
	}
	return as(env, "human:test"), get(t, env, 3), get(t, env, 1), get(t, env, 2), get(t, env, 4)
}

func TestChoosingAWayRejectsTheOthersAndClosesTheCard(t *testing.T) {
	env, h, a, b, think := chosen(t)
	if code := Decide(context.Background(), env, []string{strconv.Itoa(a.ID)}, true); code != cli.ExitOK {
		t.Fatalf("exit %d", code)
	}
	for _, c := range []struct {
		id     int
		status string
		why    string
	}{{a.ID, "approved", ""}, {b.ID, "rejected", "anderer Weg gewählt: Alt behalten"}, {think.ID, "rejected", "anderer Weg gewählt: Alt behalten"}, {h.ID, "rejected", "Weg gewählt: Alt behalten"}} {
		if p := get(t, env, c.id); p.Status != c.status || p.RejectReason != c.why {
			t.Errorf("#%d: %s %q, want %s %q", c.id, p.Status, p.RejectReason, c.status, c.why)
		}
	}
	es, _ := journal.Read(env.Paths.Journal, time.Time{})
	for _, want := range []string{fmt.Sprintf("#%d: anderer Weg gewählt: Alt behalten", b.ID), fmt.Sprintf("#%d: Weg gewählt: Alt behalten", h.ID)} {
		if !slices.ContainsFunc(es, func(e journal.Entry) bool {
			return e.Verb == "reject" && e.Result == "info" && e.Detail == want && e.Actor == "human:test"
		}) {
			t.Errorf("the journal lacks %q: %+v", want, es)
		}
	}
	if code := Decide(context.Background(), env, []string{strconv.Itoa(b.ID)}, true); code == cli.ExitOK {
		t.Error("a second way was approved")
	}
}

func TestChoosingTheTicketWayIsApprovingTheCardAndRejectsTheRest(t *testing.T) {
	env, h, a, b, think := chosen(t)
	if code := Decide(context.Background(), env, []string{strconv.Itoa(h.ID)}, true); code != cli.ExitOK {
		t.Fatalf("exit %d", code)
	}
	if p := get(t, env, h.ID); p.Status != "approved" || p.RejectReason != "" {
		t.Errorf("the card: %+v", p)
	}
	for _, p := range []*proposal.Proposal{a, b, think} {
		if q := get(t, env, p.ID); q.Status != "rejected" || q.RejectReason != "anderer Weg gewählt: Als Aufgabe weitergeben" {
			t.Errorf("#%d: %s %q", p.ID, q.Status, q.RejectReason)
		}
	}
}

func TestRejectingTheCardRejectsEveryWay(t *testing.T) {
	env, h, a, b, think := chosen(t)
	if code := Decide(context.Background(), env, []string{strconv.Itoa(h.ID), "zu", "teuer"}, false); code != cli.ExitOK {
		t.Fatalf("exit %d", code)
	}
	if p := get(t, env, h.ID); p.Status != "rejected" || p.RejectReason != "zu teuer" {
		t.Errorf("the card keeps the owner's words: %+v", p)
	}
	for _, p := range []*proposal.Proposal{a, b, think} {
		if q := get(t, env, p.ID); q.Status != "rejected" || q.RejectReason != "Karte abgelehnt, kein Weg gewählt" {
			t.Errorf("#%d: %s %q", p.ID, q.Status, q.RejectReason)
		}
	}
	if open, _ := proposal.List(cli.InboxDir(env), true); len(open) != 0 {
		t.Errorf("%d stay open", len(open))
	}
}

// a red run's two ways (#853) stay both: the restore is still wanted after a roll-forward that is red
func TestADecidedCardsWaysDoNotExcludeEachOther(t *testing.T) {
	env, st, _, _ := wahlBox(t)
	w := repairs(t, env, st, 2)
	red := &proposal.Proposal{ID: 3, Actor: "ops", Reason: "Update", Status: "approved", Outcome: "rolled-back", Wahl: []proposal.Weg{{Label: "Sicherung zurückspielen", Vorschlag: w[0].ID}, {Label: "Nach vorne reparieren", Vorschlag: w[1].ID}}}
	if err := proposal.Save(cli.InboxDir(env), red); err != nil {
		t.Fatal(err)
	}
	if code := Decide(context.Background(), as(env, "human:test"), []string{"2"}, true); code != cli.ExitOK {
		t.Fatalf("exit %d", code)
	}
	if p := get(t, env, 1); p.Status != "open" {
		t.Errorf("the restore was closed by the roll-forward: %+v", p)
	}
	if p := get(t, env, 3); p.Status != "approved" || p.RejectReason != "" {
		t.Errorf("the run's card was changed: %+v", p)
	}
}
