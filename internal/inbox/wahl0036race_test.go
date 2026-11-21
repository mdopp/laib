package inbox

import (
	"bytes"
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/sysd"
)

// a proposal that takes the card's number between its guess and its filing: the card is refused and closed
func TestACardThatLostItsNumberToAnotherProposalIsNotLeftOpen(t *testing.T) {
	env, st, _, _ := wahlBox(t)
	w := repairs(t, env, st, 2)
	real, armed := sysd.Run, true
	t.Cleanup(func() { sysd.Run = real })
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if armed && name == "git" && slices.Contains(args, "status") {
			armed = false
			proposal.Save(cli.InboxDir(env), &proposal.Proposal{ID: 3, Actor: "pi", Reason: "dazwischen", Status: "rejected"})
		}
		return real(ctx, name, args...)
	}
	if out, code := anlegen(env, "Etwas ist los.", "A|a.|"+strconv.Itoa(w[0].ID), "B|b.|"+strconv.Itoa(w[1].ID)); code == cli.ExitOK || !strings.Contains(out, "andere Karte kam dazwischen") {
		t.Fatalf("exit %d: %s", code, out)
	}
	if p := get(t, env, 4); p.Status != "rejected" || len(p.Wahl) != 0 {
		t.Errorf("the card that named the wrong number: %+v", p)
	}
}

func TestWaysAfterALeadingDashesAreNotTakenForFlags(t *testing.T) {
	env, st, _, _ := wahlBox(t)
	w := repairs(t, env, st, 2)
	var out bytes.Buffer
	e := *env
	e.Out, e.ErrOut = &out, &out
	if code := Wahl(context.Background(), &e, []string{"anlegen", "--", "A|a.|" + strconv.Itoa(w[0].ID), "B|b.|" + strconv.Itoa(w[1].ID)}); code == cli.ExitOK || !strings.Contains(out.String(), "die Felder für Menschen") {
		t.Fatalf("%d %q", code, out.String())
	}
}
