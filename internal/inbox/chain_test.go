package inbox

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/aufgaben"
	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// chainBox: the golden basic state with an owner's key WHO_MAIL, a secrets
// file, an inbox and a journal; the host faked (git and systemctl answer nothing).
func chainBox(t *testing.T) (*cli.Env, *state.State, *bytes.Buffer) {
	t.Helper()
	real := sysd.Run
	t.Cleanup(func() { sysd.Run = real })
	sysd.Run = func(context.Context, string, ...string) (string, error) { return "", os.ErrNotExist }
	home := t.TempDir()
	stDir := filepath.Join(home, "state")
	if err := os.CopyFS(stDir, os.DirFS("../../testdata/golden/basic/state")); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(stDir, "services", "whoami", "laib.yml")
	b, _ := os.ReadFile(f)
	os.WriteFile(f, bytes.Replace(b, []byte("secrets:\n"), []byte("secrets:\n  - name: WHO_MAIL\n    external: true\n    optional: true\n    owner: {label: \"Whoami: Post\", warum: W, hilfe: H}\n"), 1), 0o644)
	var out bytes.Buffer
	env := &cli.Env{Actor: "human:test", Out: &out, ErrOut: &out, Paths: boxfs.Paths{Home: home, State: stDir, Journal: filepath.Join(home, "journal"),
		Key: filepath.Join(home, "age.key"), Secrets: filepath.Join(home, "secrets.age")}}
	if _, err := secrets.EnsureKey(env.Paths.Key); err != nil {
		t.Fatal(err)
	}
	if err := secrets.Save(env.Paths.Secrets, env.Paths.Key, map[string]string{"OTHER": "x"}); err != nil {
		t.Fatal(err)
	}
	st, err := cli.LoadState(env)
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(cli.InboxDir(env), 0o755)
	return env, st, &out
}

func save(t *testing.T, env *cli.Env, p *proposal.Proposal) *proposal.Proposal {
	t.Helper()
	if err := proposal.Save(cli.InboxDir(env), p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAWaitIsOneLaibCanMeasure(t *testing.T) {
	_, st, _ := chainBox(t)
	for _, ok := range [][]string{nil, {"vorschlag:1", "aufgabe:12", "version:2.24.0", "version:v10.0.1", "schluessel:WHO_MAIL"}} {
		if err := checkWaits(st, ok); err != nil {
			t.Errorf("%v: %v", ok, err)
		}
	}
	for _, bad := range []string{"vorschlag:0", "vorschlag:x", "vorschlag:12345678", "aufgabe:", "version:2.24", "version:2.24.0-rc", "schluessel:who_mail", "schluessel:NOPE", "schluessel:WHOAMI_TOKEN", "anderes:1", " vorschlag:1"} {
		if err := checkWaits(st, []string{"vorschlag:1", bad}); err == nil || !strings.Contains(err.Error(), bad) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestAtLeast(t *testing.T) {
	for _, c := range []struct {
		have, want string
		ok         bool
	}{{"v2.24.0", "2.24.0", true}, {"2.24.1", "v2.24.0", true}, {"2.25.0", "2.24.9", true}, {"3.0.0", "2.99.99", true}, {"10.0.0", "9.0.0", true},
		{"2.23.9", "2.24.0", false}, {"2.24.0", "2.24.1", false}, {"1.99.99", "2.0.0", false}, {"dev", "0.0.1", false}, {"abc1234", "0.0.1", false}} {
		if atLeast(c.have, c.want) != c.ok {
			t.Errorf("atLeast(%q, %q) is %v", c.have, c.want, !c.ok)
		}
	}
}

func TestWhatAProposalWaitsFor(t *testing.T) {
	env, st, _ := chainBox(t)
	was := cli.Version
	t.Cleanup(func() { cli.Version = was })
	cli.Version = "v2.24.0"
	link := func(n string) string { return "https://laib.example.home/inbox#" + n }
	save(t, env, &proposal.Proposal{ID: 1, Status: "open"})
	save(t, env, &proposal.Proposal{ID: 2, Status: "approved", Outcome: "waiting"})
	save(t, env, &proposal.Proposal{ID: 3, Status: "approved", Outcome: "applied"})
	save(t, env, &proposal.Proposal{ID: 4, Status: "approved"}) // a run applied it, not settled yet
	save(t, env, &proposal.Proposal{ID: 5, Status: "rejected", Outcome: "rejected"})
	save(t, env, &proposal.Proposal{ID: 6, Status: "stale"})
	save(t, env, &proposal.Proposal{ID: 7, Status: "approved", Outcome: "rolled-back"})
	save(t, env, &proposal.Proposal{ID: 8, Status: "stale", Refiled: 9})
	save(t, env, &proposal.Proposal{ID: 9, Status: "stale", Refiled: 1})
	aufgaben.Update(context.Background(), env.Paths.State, "test", func(l []aufgaben.Aufgabe) ([]aufgaben.Aufgabe, string, error) {
		l, _ = aufgaben.Add(l, aufgaben.Aufgabe{Titel: "Offen", Weg: &aufgaben.Weg{Schluessel: "WHO_MAIL"}}, "t", time.Now())
		l, _ = aufgaben.Add(l, aufgaben.Aufgabe{Titel: "Erledigt"}, "t", time.Now())
		l, _ = aufgaben.Add(l, aufgaben.Aufgabe{Titel: "Ohne Weg"}, "t", time.Now())
		aufgaben.Done(l, 2, "t", "", time.Now())
		return l, "add", nil
	})
	for _, c := range []struct {
		wait string
		want []Wait
	}{
		{"vorschlag:1", []Wait{{Text: "wartet auf Vorschlag #1", Link: link("1")}}},
		{"vorschlag:2", []Wait{{Text: "wartet auf Vorschlag #2", Link: link("2")}}},
		{"vorschlag:3", nil},
		{"vorschlag:4", nil},
		{"vorschlag:5", []Wait{{Text: "wartet auf Vorschlag #5, der abgelehnt ist: diesen ablehnen oder neu vorschlagen", Link: link("5"), Nie: true}}},
		{"vorschlag:6", []Wait{{Text: "wartet auf Vorschlag #6, der veraltet ist: diesen ablehnen oder neu vorschlagen", Link: link("6"), Nie: true}}},
		{"vorschlag:7", []Wait{{Text: "wartet auf Vorschlag #7, der zurückgenommen ist: diesen ablehnen oder neu vorschlagen", Link: link("7"), Nie: true}}},
		{"vorschlag:8", []Wait{{Text: "wartet auf Vorschlag #1", Link: link("1")}}},
		{"vorschlag:99", []Wait{{Text: "wartet auf Vorschlag #99, den es nicht gibt: diesen ablehnen oder neu vorschlagen", Nie: true}}},
		{"version:2.24.0", nil},
		{"version:v2.23.1", nil},
		{"version:2.24.1", []Wait{{Text: "wartet auf Laib 2.24.1 (läuft: v2.24.0)"}}},
		{"version:v3.0.0", []Wait{{Text: "wartet auf Laib 3.0.0 (läuft: v2.24.0)"}}},
		{"schluessel:OTHER", nil},
		{"schluessel:WHO_MAIL", []Wait{{Text: "wartet auf den Schlüssel „Whoami: Post“", Link: "https://laib.example.home/schluessel?name=WHO_MAIL"}}},
		{"schluessel:GONE_KEY", []Wait{{Text: "wartet auf den Schlüssel „GONE_KEY“", Link: "https://laib.example.home/schluessel?name=GONE_KEY"}}},
		{"aufgabe:1", []Wait{{Text: "wartet auf Box-Aufgabe #1 „Offen“", Link: "https://laib.example.home/schluessel?name=WHO_MAIL"}}},
		{"aufgabe:2", nil},
		{"aufgabe:3", []Wait{{Text: "wartet auf Box-Aufgabe #3 „Ohne Weg“"}}},
		{"aufgabe:4", []Wait{{Text: "wartet auf Box-Aufgabe #4, die es nicht gibt: diesen ablehnen oder neu vorschlagen", Nie: true}}},
	} {
		if got := Waits(env, st, &proposal.Proposal{WartetAuf: []string{c.wait}}); !slices.Equal(got, c.want) {
			t.Errorf("%s: %+v, want %+v", c.wait, got, c.want)
		}
	}
	if got := Waits(env, st, &proposal.Proposal{WartetAuf: []string{"vorschlag:1", "vorschlag:3", "aufgabe:3"}}); len(got) != 2 {
		t.Errorf("every wait that is not over, in order: %+v", got)
	}
	if got := WaitsOf(env, &proposal.Proposal{Status: "open", WartetAuf: []string{"vorschlag:1"}}); len(got) != 1 {
		t.Errorf("an open card shows its wait: %+v", got)
	}
	if got := WaitsOf(env, &proposal.Proposal{Status: "approved", WartetAuf: []string{"vorschlag:1"}}); got != nil {
		t.Errorf("a decided card waits for nothing: %+v", got)
	}
	var out bytes.Buffer
	printWaits(&cli.Env{Paths: env.Paths, Out: &out}, &proposal.Proposal{Status: "open", WartetAuf: []string{"vorschlag:1", "aufgabe:3"}})
	if out.String() != "     wartet auf Vorschlag #1 "+link("1")+"\n     wartet auf Box-Aufgabe #3 „Ohne Weg“ \n" {
		t.Errorf("the listing: %q", out.String())
	}
}

func TestAnApproveOfAWaitingCardHoldsIt(t *testing.T) {
	env, st, out := chainBox(t)
	save(t, env, &proposal.Proposal{ID: 1, Status: "open"})
	free := save(t, env, &proposal.Proposal{ID: 2, Actor: "ops", Status: "open", Reason: "frei"})
	if held, code := hold(env, st, free, "human:test"); held || code != cli.ExitOK || free.Freigabe != "" {
		t.Fatalf("nothing missing: not held, %v %d %q", held, code, free.Freigabe)
	}
	p := save(t, env, &proposal.Proposal{ID: 3, Actor: "ops:x", Status: "open", Reason: "danach", WartetAuf: []string{"vorschlag:1"}})
	if held, code := hold(env, st, p, "ops"); !held || code != cli.ExitRefused || p.Freigabe != "" || !strings.Contains(out.String(), "own proposal") {
		t.Fatalf("a hand never approves its own, not even in advance: %v %d %q %s", held, code, p.Freigabe, out)
	}
	out.Reset()
	if held, code := hold(env, st, p, "human:anna"); !held || code != cli.ExitOK {
		t.Fatalf("held: %v %d", held, code)
	}
	if got, _ := proposal.Get(cli.InboxDir(env), 3); got.Freigabe != "human:anna" || got.Status != "open" {
		t.Errorf("held stays open, approved in advance: %+v", got)
	}
	if out.String() != "Vorschlag #3 freigegeben: Laib wendet ihn an, sobald nichts mehr fehlt (wartet auf Vorschlag #1).\n" {
		t.Errorf("it says so: %q", out.String())
	}
	es, _ := journal.Read(env.Paths.Journal, time.Time{})
	if len(es) != 1 || es[0].Actor != "human:anna" || es[0].Verb != "approve" || es[0].Reason != "danach" || es[0].Detail != "#3 von ops:x freigegeben, wartet auf Vorschlag #1" {
		t.Errorf("the journal: %+v", es)
	}
}

// Release tries exactly the held cards whose wait is over, oldest first;
// here no state repo merges them, so each try is a red journal line.
func TestReleaseTriesWhatNoLongerWaits(t *testing.T) {
	env, _, _ := chainBox(t)
	save(t, env, &proposal.Proposal{ID: 1, Status: "open"})
	save(t, env, &proposal.Proposal{ID: 2, Actor: "ops", Tier: "apply", Status: "open", Freigabe: "human:a"})
	save(t, env, &proposal.Proposal{ID: 3, Actor: "ops", Tier: "apply", Status: "open", Freigabe: "human:a", WartetAuf: []string{"vorschlag:1"}})
	save(t, env, &proposal.Proposal{ID: 4, Actor: "ops", Tier: "apply", Status: "open"})
	save(t, env, &proposal.Proposal{ID: 5, Actor: "ops", Tier: "apply", Status: "approved", Freigabe: "human:a"})
	save(t, env, &proposal.Proposal{ID: 6, Actor: "ops", Tier: "apply", Status: "open", Freigabe: "human:b", WartetAuf: []string{"vorschlag:5"}})
	Release(context.Background(), env)
	es, _ := journal.Read(env.Paths.Journal, time.Time{})
	var tried []string
	for _, e := range es {
		if e.Result == "red" && e.Actor == "laib" {
			tried = append(tried, strings.SplitN(e.Detail, " ", 2)[0])
			if !strings.Contains(e.Detail, "war freigegeben, nichts fehlt mehr, aber: ") || strings.Contains(e.Detail, "usage") {
				t.Errorf("why: %q", e.Detail)
			}
		}
	}
	if !slices.Equal(tried, []string{"#2", "#6"}) {
		t.Errorf("tried %v, want #2 then #6: %+v", tried, es)
	}
}

func TestANewerValuesCardSupersedesTheOlder(t *testing.T) {
	env, st, _ := chainBox(t)
	img := proposal.Change{Service: "whoami", Key: "IMAGE"}
	tag := proposal.Change{Service: "whoami", Key: "TAG"}
	other := proposal.Change{Service: "caddy", Key: "IMAGE"}
	save(t, env, &proposal.Proposal{ID: 1, Status: "open", Kind: "values", Changes: []proposal.Change{img}})
	save(t, env, &proposal.Proposal{ID: 2, Status: "open", Kind: "values", Changes: []proposal.Change{img, tag}})
	save(t, env, &proposal.Proposal{ID: 3, Status: "open", Kind: "values", Changes: []proposal.Change{other}})
	save(t, env, &proposal.Proposal{ID: 4, Status: "open", Kind: "tree", Changes: []proposal.Change{img}})
	save(t, env, &proposal.Proposal{ID: 5, Status: "open", Kind: "values"})
	save(t, env, &proposal.Proposal{ID: 6, Status: "approved", Kind: "values", Changes: []proposal.Change{img}})
	p := save(t, env, &proposal.Proposal{ID: 7, Status: "open", Kind: "values", Changes: []proposal.Change{img}})
	save(t, env, &proposal.Proposal{ID: 8, Status: "open", Kind: "values", Changes: []proposal.Change{img}})
	supersede(context.Background(), env, st, &proposal.Proposal{ID: 7, Kind: "tree", Changes: p.Changes})
	if q, _ := proposal.Get(cli.InboxDir(env), 1); q.Status != "open" {
		t.Fatal("only a values card supersedes")
	}
	supersede(context.Background(), env, st, p)
	for id, want := range map[int]string{1: "stale", 2: "open", 3: "open", 4: "open", 5: "open", 6: "approved", 7: "open", 8: "open"} {
		if q, _ := proposal.Get(cli.InboxDir(env), id); q.Status != want {
			t.Errorf("#%d is %s, want %s", id, q.Status, want)
		}
	}
	q, _ := proposal.Get(cli.InboxDir(env), 1)
	if q.Refiled != 7 || q.StaleReason != "abgelöst durch #7, der dieselben Werte neuer setzt" || q.DecidedBy != "laib" || q.Decided.IsZero() {
		t.Errorf("the superseded card names its successor: %+v", q)
	}
	es, _ := journal.Read(env.Paths.Journal, time.Time{})
	if len(es) != 1 || es[0].Detail != "#1 abgelöst durch #7" || es[0].Verb != "reject" {
		t.Errorf("the journal: %+v", es)
	}
}

func TestAnchorStoresWhatAProposalWaitsFor(t *testing.T) {
	env, _, _ := chainBox(t)
	p := &proposal.Proposal{ID: 1, Status: "open"}
	if Anchor(cli.InboxDir(env), p, proposal.Anchor{}); func() bool { _, err := proposal.Get(cli.InboxDir(env), 1); return err == nil }() {
		t.Fatal("nothing to anchor: nothing saved")
	}
	p.WartetAuf = []string{"vorschlag:2"}
	Anchor(cli.InboxDir(env), p, proposal.Anchor{})
	if q, err := proposal.Get(cli.InboxDir(env), 1); err != nil || !slices.Equal(q.WartetAuf, []string{"vorschlag:2"}) {
		t.Errorf("saved with what it waits for: %+v %v", q, err)
	}
}

func TestDecideTakesOneIDAndProposeRefusesAWaitItCannotMeasure(t *testing.T) {
	env, _, out := chainBox(t)
	for _, args := range [][]string{nil, {"1", "2"}} {
		out.Reset()
		if Decide(context.Background(), env, args, true) == cli.ExitOK || !strings.Contains(out.String(), "usage") {
			t.Errorf("approve %v: %s", args, out)
		}
	}
	if code := Propose(context.Background(), env, []string{"--wartet", "schluessel:NOPE", "x"}); code != cli.ExitRefused {
		t.Errorf("an unknown key: %d", code)
	}
	if code := Propose(context.Background(), env, []string{"--wartet", "vorschlag:1", "x"}); code == cli.ExitRefused || code == cli.ExitOK {
		t.Errorf("a wait laib measures passes on to the proposal (none here: no repo): %d", code)
	}
}
