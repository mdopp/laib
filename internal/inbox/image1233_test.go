package inbox

import (
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
)

// Ablauf L-57. ADR 0035 (#1233): a new image digest is checked by the
// kernel's own CheckImage when it is proposed and again before the run; a
// pin of what runs, a value without a digest, another key or an added value
// are not asked about, and a refusal is said in the owner's words.
func TestAnImageIsCheckedWhenProposedAndBeforeItRuns(t *testing.T) {
	realC, realR, realV := CheckImage, Runs, ImageVersion
	t.Cleanup(func() { CheckImage, Runs, ImageVersion = realC, realR, realV })
	var asked []string
	CheckImage = func(old, image string) string {
		asked = append(asked, image)
		if strings.HasSuffix(image, "@sha256:bad") {
			return "nicht signiert"
		}
		return ""
	}
	Runs = func(image string) bool { return strings.HasSuffix(image, "@sha256:runs") }
	ImageVersion = func(image string) string { return map[string]string{"x:1@sha256:good": "1.1"}[image] }
	img := func(op, key, old, new string) proposal.Change {
		return proposal.Change{Op: op, Service: "homes", Key: key, Old: old, New: new}
	}
	good := img("~", "IMAGE", "x:1", "x:1@sha256:good")
	if err := judgeImage(&good); err != nil || good.NewVersion != "1.1" || good.Pin {
		t.Fatalf("a good digest: %v %+v", err, good)
	}
	bad := img("~", "IMAGE", "x:1", "x:1@sha256:bad")
	if err := judgeImage(&bad); err == nil || err.Error() != "homes: nicht signiert, Laib schlägt es nicht vor (ADR 0034)" {
		t.Fatalf("a bad digest proposed: %v", err)
	}
	pin := img("~", "IMAGE", "x:1", "x:1@sha256:runs")
	if err := judgeImage(&pin); err != nil || !pin.Pin {
		t.Fatalf("a pin of what runs: %v %+v", err, pin)
	}
	asked = nil
	for _, c := range []proposal.Change{img("+", "IMAGE", "", "x:1@sha256:bad"), img("~", "TOKEN", "a", "b@sha256:bad"), img("~", "IMAGE", "x:1", "x:2"), pin} {
		if err := judgeImage(&c); err != nil {
			t.Errorf("%+v refused: %v", c, err)
		}
	}
	if len(asked) != 0 {
		t.Fatalf("checked what is no new digest: %q", asked)
	}
	ok := &proposal.Proposal{Changes: []proposal.Change{good, pin, img("~", "TOKEN", "a", "b@sha256:bad")}}
	if why := checkBatch([]*proposal.Proposal{ok}); why != "" {
		t.Fatalf("a run of good digests refused: %q", why)
	}
	if why := checkBatch([]*proposal.Proposal{ok, {Changes: []proposal.Change{bad}}}); why != "weil Laib das neue Programm für homes vor dem Einspielen geprüft hat und es nicht stimmt: nicht signiert" {
		t.Fatalf("the run's reason: %q", why)
	}
	env := &cli.Env{Actor: "human:test", Paths: boxfs.Paths{Journal: t.TempDir()}}
	if code, why := checked(env, nil, []*proposal.Proposal{ok}, cli.ExitOK, "vorher"); code != cli.ExitOK || why != "vorher" {
		t.Fatalf("a good run goes on as it was: %d %q", code, why)
	}
	code, why := checked(env, nil, []*proposal.Proposal{{Changes: []proposal.Change{bad}}}, cli.ExitOK, "vorher")
	es, _, _ := journal.ReadCounted(env.Paths.Journal, time.Now().Add(-time.Hour))
	if code != cli.ExitRefused || !strings.Contains(why, "nicht signiert") || len(es) != 1 || es[0].Result != "red" || es[0].Detail != why {
		t.Fatalf("a bad run is refused and journaled: %d %q %+v", code, why, es)
	}
	CheckImage = nil
	if why := checkBatch([]*proposal.Proposal{{Changes: []proposal.Change{bad}}}); why != "" {
		t.Fatalf("without a check: %q", why)
	}
}
