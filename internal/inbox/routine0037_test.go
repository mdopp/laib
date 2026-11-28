package inbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/review"
	"github.com/mdopp/laib/internal/state"
)

const (
	d1 = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	d2 = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

// routineProposal is what the Operator's versionen files for one new image:
// a values change of IMAGE, the review's one reason for it.
func routineProposal(image string) *proposal.Proposal {
	return &proposal.Proposal{Actor: "ops", Kind: "values", Files: []string{"services/immich/values.yml"},
		Reasons:  []string{review.NewImage + "immich (… → …): neue Software nur auf deinen Tipp (ADR 0034)"},
		Warnings: []string{review.NewImage + "immich (… → …), dessen Daten es migrieren kann: vorher sichert Laib immich"},
		Changes:  []proposal.Change{{Op: "~", Service: "immich", Key: "IMAGE", Old: image + "@" + d1, New: image + "@" + d2}}}
}

func seenAt(t *testing.T, digest string, at time.Time) {
	t.Helper()
	old := Seen
	t.Cleanup(func() { Seen = old })
	Seen = func(_, service, key string) (string, time.Time) {
		if service == "immich" && key == "IMAGE" {
			return digest, at
		}
		return "", time.Time{}
	}
}

// ADR 0037: only new digests of images running services already have, each
// the newest laib updates found, a third party's after its soak (three days,
// or the template's) unless a security fix, our own at once; anything else
// — a key, a route, a script, a port, policy or box.yml, a template, another
// hand, the switch off — stays a card, with the reason. Ablauf L-70, L-72, L-73, L-75
func TestRoutineWhy(t *testing.T) {
	now := time.Date(2026, 11, 28, 5, 30, 0, 0, time.UTC)
	const third = "ghcr.io/immich-app/immich-server:release"
	st := &state.State{Services: map[string]*state.Service{"immich": {Name: "immich"}}}
	seenAt(t, d2, now.Add(-73*time.Hour))
	if why := routineWhy(st, routineProposal(third), "", now); why != "" {
		t.Fatalf("a soaked third-party digest: %s", why)
	}
	for name, c := range map[string]struct {
		edit func(p *proposal.Proposal, st *state.State)
		want string
	}{
		"switch off":    {func(_ *proposal.Proposal, st *state.State) { st.Box.Updates.Auto = "aus" }, "updates.auto: aus"},
		"another hand":  {func(p *proposal.Proposal, _ *state.State) { p.Actor = "claude" }, "nur der Operator"},
		"a template":    {func(p *proposal.Proposal, _ *state.State) { p.Kind = "template-update" }, "nur der Operator"},
		"nothing":       {func(p *proposal.Proposal, _ *state.State) { p.Changes = nil }, "nur der Operator"},
		"a key":         {func(p *proposal.Proposal, _ *state.State) { p.Mints = []string{"bote"} }, "nur der Operator"},
		"a new service": {func(p *proposal.Proposal, _ *state.State) { p.Adds = []string{"x"} }, "nur der Operator"},
		"a removal":     {func(p *proposal.Proposal, _ *state.State) { p.Removes = []string{"x"} }, "nur der Operator"},
		"a public route": {func(p *proposal.Proposal, _ *state.State) {
			p.Reasons = append(p.Reasons, "immich ist ohne Anmeldung aus dem Internet erreichbar")
		}, "ohne Anmeldung"},
		"a host script": {func(p *proposal.Proposal, _ *state.State) {
			p.Reasons = []string{"führt einen Befehl direkt auf der Box aus"}
		}, "direkt auf der Box"},
		"a warning":      {func(p *proposal.Proposal, _ *state.State) { p.Warnings = []string{"ein zweiter Dienst für Fotos"} }, "zweiter Dienst"},
		"box.yml":        {func(p *proposal.Proposal, _ *state.State) { p.Files = append(p.Files, "box.yml") }, "box.yml"},
		"policy.yml":     {func(p *proposal.Proposal, _ *state.State) { p.Files = []string{"policy.yml"} }, "policy.yml"},
		"a unit":         {func(p *proposal.Proposal, _ *state.State) { p.Files = []string{"services/immich/immich.container"} }, "immich.container"},
		"another value":  {func(p *proposal.Proposal, _ *state.State) { p.Changes[0].Key = "PORT" }, "immich PORT"},
		"a removed one":  {func(p *proposal.Proposal, _ *state.State) { p.Changes[0].Op = "-" }, "immich IMAGE"},
		"a pin":          {func(p *proposal.Proposal, _ *state.State) { p.Changes[0].Pin = true }, "immich IMAGE"},
		"another digest": {func(p *proposal.Proposal, _ *state.State) { p.Changes[0].New = third + "@" + d1 }, "nicht der neueste"},
		"its own soak":   {func(_ *proposal.Proposal, st *state.State) { st.Services["immich"].Contract.Soak = 7 }, "wartet noch bis"},
	} {
		p, st := routineProposal(third), &state.State{Services: map[string]*state.Service{"immich": {Name: "immich"}}}
		c.edit(p, st)
		if why := routineWhy(st, p, "", now); !strings.Contains(why, c.want) {
			t.Errorf("%s: %q, want it held with %q", name, why, c.want)
		}
	}
	seenAt(t, d2, now.Add(-71*time.Hour)) // the soak is not over
	if why := routineWhy(st, routineProposal(third), "", now); !strings.Contains(why, "immich wartet noch bis") {
		t.Fatalf("within the soak: %q", why)
	}
	sec := routineProposal(third)
	sec.Routine = "sicherheit"
	if why := routineWhy(st, sec, "", now); why != "" {
		t.Fatalf("a security fix skips the soak: %q", why)
	}
	seenAt(t, d2, now) // our own: no soak, its CI and signature instead
	if why := routineWhy(st, routineProposal("ghcr.io/mdopp/homes:latest"), "", now); why != "" {
		t.Fatalf("our own image: %q", why)
	}
	st.Box.Updates.Auto = "an"
	if why := routineWhy(st, routineProposal("ghcr.io/mdopp/homes:latest"), "", now); why != "" {
		t.Fatalf("updates.auto: an is on: %q", why)
	}
}

// Soak: three days, or the template's own days; a service laib does not know three.
func TestSoakIsThreeDaysOrTheTemplates(t *testing.T) {
	st := &state.State{Services: map[string]*state.Service{"a": {}, "b": {Contract: state.Contract{Soak: 7}}}}
	for svc, want := range map[string]time.Duration{"a": 72 * time.Hour, "b": 168 * time.Hour, "x": 72 * time.Hour} {
		if got := Soak(st, svc); got != want {
			t.Errorf("%s: %v, want %v", svc, got, want)
		}
	}
}

// ADR 0037: a routine laib may not let pass stays a card — saved with the
// reason, its word cleared — and so does one with another word or from a
// program behind the door; without --routine only the dial decides, and the
// journal names how laib approved. Ablauf L-75
func TestARefusedRoutineStaysACardWithItsReason(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	seenAt(t, d2, now.Add(-time.Hour)) // inside the soak: routineWhy says why
	st := &state.State{Services: map[string]*state.Service{"immich": {Name: "immich"}}}
	env := &cli.Env{Actor: "ops", Paths: boxfs.Paths{Home: t.TempDir()}}
	os.MkdirAll(cli.InboxDir(env), 0o755)
	for _, c := range []struct{ word, ceiling, want string }{
		{"normal", "", "nicht von selbst eingespielt: immich wartet noch bis "},
		{"sicherheit", "propose", "nicht von selbst eingespielt: --routine ist normal oder sicherheit, von keinem Programm hinter der Tür (ADR 0037)"},
		{"vielleicht", "", "nicht von selbst eingespielt: --routine ist normal oder sicherheit, von keinem Programm hinter der Tür (ADR 0037)"},
	} {
		p := routineProposal("ghcr.io/immich-app/immich-server:release")
		p.ID = 4
		env.Ceiling = c.ceiling
		auto, code := itself(ctx, env, st, p, c.word)
		saved, err := proposal.Get(cli.InboxDir(env), 4)
		if auto || code != cli.ExitOK || err != nil || saved.Routine != "" || !strings.HasPrefix(saved.Reasons[len(saved.Reasons)-1], c.want) || len(saved.Reasons) != 2 {
			t.Errorf("%s/%s: auto %v, exit %d: %+v %v", c.word, c.ceiling, auto, code, saved, err)
		}
	}
	p := routineProposal("x")
	if auto, _ := itself(ctx, env, st, p, ""); auto || len(p.Reasons) != 1 || p.Routine != "" {
		t.Fatalf("without --routine: %+v", p)
	}
	if how(p) != "per autonomy" {
		t.Fatalf("the dial: %q", how(p))
	}
	p.Routine = "sicherheit"
	if how(p) != "als Routine (sicherheit, ADR 0037)" {
		t.Fatalf("a routine: %q", how(p))
	}
	env.Paths.Home = filepath.Join(t.TempDir(), "gone") // the card cannot be saved: said, not swallowed
	if auto, code := itself(ctx, env, st, routineProposal("x"), "normal"); !auto || code == cli.ExitOK {
		t.Fatalf("an unsaved card: auto %v, exit %d", auto, code)
	}
}

// itself without --routine is the dial (#609): laib approves a class the
// dial sets auto, and never one the review named, one with a warning, a
// human's or one from behind the door. Here each case misses exactly one
// of them; laib's approval would need a state repo and so would answer.
func TestTheDialLeavesEachMissingConditionToAPerson(t *testing.T) {
	st := &state.State{Policy: state.Policy{Autonomy: map[string]string{"values": "auto"}}}
	for name, c := range map[string]struct {
		class, actor, ceiling string
		warnings              []string
	}{
		"the review named it": {"human", "ops", "", nil},
		"a warning":           {"values", "ops", "", []string{"ein zweiter Dienst"}},
		"a human's":           {"values", "human:mdopp", "", nil},
		"behind the door":     {"values", "ops", "propose", nil},
	} {
		env := &cli.Env{Actor: c.actor, Ceiling: c.ceiling, Paths: boxfs.Paths{Home: t.TempDir(), State: t.TempDir()}}
		p := &proposal.Proposal{ID: 1, Actor: c.actor, Class: c.class, Warnings: c.warnings}
		if auto, code := itself(context.Background(), env, st, p, ""); auto || code != cli.ExitOK {
			t.Errorf("%s: laib decided it itself (exit %d)", name, code)
		}
	}
}
