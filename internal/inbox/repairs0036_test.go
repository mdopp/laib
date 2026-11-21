package inbox

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/state"
)

// The allowlist of repairs, every form, now that this package's tests run it (the mutants of
// scripts/ratchet.py mutation): what is on it runs, what is near it does not.
func TestRepairableIsExactlyTheAllowlist(t *testing.T) {
	_, st, _, _ := wahlBox(t)
	st.Services["vault"] = &state.Service{Name: "vault", Contract: state.Contract{
		Data:    []state.DataPath{{Path: t.TempDir()}},
		Secrets: []state.Secret{{Name: "TOKEN"}, {Name: "MAIL", External: true}, {Name: "DB", DataBound: true}},
	}}
	for _, c := range []struct {
		line string
		want bool
	}{
		{"apply", true}, {"backup", true}, {"backup check", true}, {"operator enable", true}, {"operator disable", false}, {"apply now", false}, {"remove whoami", false},
		{"secret zurueck TOKEN", true}, {"secret zurueck MAIL", false}, {"secret zurueck DB", false}, {"secret zurueck NOPE", false}, {"secret setzen TOKEN", false}, {"secret zurueck", false}, {"geheim zurueck TOKEN", false},
		{"restore 4f9a2c1e vault", true}, {"restore latest vault", false}, {"restore 4f9a2c1e whoami", false}, {"restore 4f9a2c1e nope", false}, {"restore 4f9a2c1e", false}, {"restore 4f9a2c 1", false},
		{"rollback 4f9a2c1e", true}, {"rollback latest", false}, {"rollback", false}, {"rollback 4f9a2c1e 4f9a2c1e", false}, {"rollbacks 4f9a2c1e", false},
		{"restart whoami", true}, {"restart nope", false}, {"restart", false}, {"restart whoami now", false}, {"stop whoami", false},
	} {
		if got := Repairable(st, strings.Fields(c.line)); got != c.want {
			t.Errorf("Repairable(%q) = %v, want %v", c.line, got, c.want)
		}
	}
	for s, want := range map[string]bool{"4f9a2c1": true, "4f9a2c": false, strings.Repeat("a", 64): true, strings.Repeat("a", 65): false, "4F9A2C1E": false, "4f9a2c1g": false, "": false} {
		if hexID(s) != want {
			t.Errorf("hexID(%q) = %v, want %v", s, !want, want)
		}
	}
}

func TestOneFileADayPerCommandInOrder(t *testing.T) {
	env, st, _, _ := wahlBox(t)
	p, err := fileRepair(context.Background(), env, st, "Zwei Befehle", "zwei", "nichts", nil, []string{"laib restart whoami", "laib backup"})
	if err != nil || len(p.Files) != 2 || !strings.HasSuffix(p.Files[0], "-1.md") || !strings.HasSuffix(p.Files[1], "-2.md") {
		t.Fatalf("%+v %v", p, err)
	}
}

// a repair runs as the approver, and a verb that fails is on the card in plain words and in the journal
func TestARepairThatFailsSaysSoOnItsCardAndOneThatWorksSaysNothing(t *testing.T) {
	env, st, _, _ := wahlBox(t)
	ok := repairs(t, env, st, 1)[0]
	human := as(env, "human:test")
	if code := Decide(context.Background(), human, []string{strconv.Itoa(ok.ID)}, true); code != cli.ExitOK {
		t.Fatalf("exit %d", code)
	}
	if p := get(t, env, ok.ID); p.Because != "" {
		t.Errorf("a repair that worked has a reason: %q", p.Because)
	}
	bad := repairs(t, env, st, 1)[0]
	RunVerb = func(context.Context, *cli.Env, []string) int { return cli.ExitRed }
	if code := Decide(context.Background(), human, []string{strconv.Itoa(bad.ID)}, true); code != cli.ExitRed {
		t.Fatalf("exit %d", code)
	}
	if p := get(t, env, bad.ID); !strings.HasPrefix(p.Because, "fehlgeschlagen: ") {
		t.Errorf("the card: %q", p.Because)
	}
	es, _ := journal.Read(env.Paths.Journal, time.Time{})
	n := 0
	for _, e := range es {
		if e.Verb == "repair" && e.Result == "red" && strings.Contains(e.Detail, "fehlgeschlagen") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d red repair lines, want 1: %+v", n, es)
	}
}
