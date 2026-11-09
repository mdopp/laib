package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/envelope"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/sysd"
)

// privateBox is envBox with whoami marked private (ADR 0032); podman exec
// answers with a family document's text, so a test sees where it lands.
func privateBox(t *testing.T, mode string) (*cliEnv, string, *[]string) {
	t.Helper()
	env, data, calls := envBox(t)
	p := filepath.Join(env.Paths.State, "services", "whoami", "laib.yml")
	b, _ := os.ReadFile(p)
	os.WriteFile(p, append([]byte("private: "+mode+"\n"), b...), 0o644)
	if err := proposal.CommitDirty(context.Background(), env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(data, "whoami", "cfg", "brief.txt"), []byte("Versicherungsnummer 4711"), 0o644)
	run := sysd.Run
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		out, err := run(ctx, name, args...)
		if name == "podman" && len(args) > 0 && args[0] == "exec" {
			return "Versicherungsnummer 4711", nil
		}
		return out, err
	}
	return env, data, calls
}

func quiet(env *cliEnv, actor string) *cliEnv {
	return &cliEnv{Actor: actor, Paths: env.Paths, Out: io.Discard, ErrOut: io.Discard, In: strings.NewReader("x")}
}

func podmanExecs(calls []string) (n int) {
	for _, c := range calls {
		if strings.HasPrefix(c, "podman exec") {
			n++
		}
	}
	return n
}

// A sealed service (the archive) refuses every verb that reaches inside, for
// every hand, the human's included.
func TestSealedRefusesEveryVerbForEveryHand(t *testing.T) {
	env, data, calls := privateBox(t, "sealed")
	ctx := context.Background()
	file := filepath.Join(data, "whoami", "cfg", "brief.txt")
	for _, actor := range []string{"ops", "claude", "human:test"} {
		e := quiet(env, actor)
		for verb, code := range map[string]int{
			"look":  envelope.Look(ctx, e, []string{"whoami", "--", "cat", "/x"}),
			"exec":  envelope.Exec(ctx, e, []string{"whoami", "--", "cat", "/x"}),
			"read":  envelope.Read(ctx, e, []string{"whoami", file}),
			"write": envelope.Write(ctx, quiet(env, actor), []string{"whoami", file}),
		} {
			if code != cli.ExitRefused {
				t.Errorf("%s by %s on a sealed service: exit %d, want refused", verb, actor, code)
			}
		}
	}
	if n := podmanExecs(*calls); n != 0 {
		t.Fatalf("podman exec ran %d times in a sealed service", n)
	}
	if b, _ := os.ReadFile(file); string(b) != "Versicherungsnummer 4711" {
		t.Fatalf("a sealed file was written: %q", b)
	}
	if open, _ := proposal.List(cli.InboxDir(env), true); len(open) != 0 {
		t.Fatalf("a sealed service files no Einblick, got %d proposals", len(open))
	}
}

// An approval service: an agent is refused and one Einblick is filed (the
// second try reuses it); after a human's approval it looks for an hour,
// then it is refused again. The human looks without a card. The journal
// keeps the size of the output, never its text.
func TestApprovalNeedsAnEinblickForAnHour(t *testing.T) {
	env, _, calls := privateBox(t, "approval")
	ctx := context.Background()
	look := func(actor string) int {
		return envelope.Look(ctx, quiet(env, actor), []string{"whoami", "--", "cat", "/data/brief.txt"})
	}
	if code := look("ops"); code != cli.ExitRefused {
		t.Fatalf("an agent's look without an Einblick: exit %d", code)
	}
	if code := envelope.Exec(ctx, quiet(env, "ops"), []string{"whoami", "--", "cat", "/x"}); code != cli.ExitRefused {
		t.Fatalf("an agent's exec without an Einblick: exit %d", code)
	}
	if n := podmanExecs(*calls); n != 0 {
		t.Fatalf("podman exec ran %d times before the approval", n)
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	if len(open) != 1 {
		t.Fatalf("want exactly one Einblick filed, got %d", len(open))
	}
	p := open[0]
	if p.Class != "human" || !p.Boxless() || !strings.Contains(p.Does, "eine Stunde lang") {
		t.Fatalf("the Einblick: class %s boxless %v does %q", p.Class, p.Boxless(), p.Does)
	}
	if code := inbox.Approve(ctx, &cliEnv{Actor: "ops", Paths: env.Paths, Out: io.Discard, ErrOut: io.Discard}, []string{strconv.Itoa(p.ID)}); code == cli.ExitOK {
		t.Fatal("an agent approved an Einblick")
	}
	if code := inbox.Approve(ctx, quiet(env, "human:test"), []string{strconv.Itoa(p.ID)}); code != cli.ExitOK {
		t.Fatalf("the human's approval: exit %d", code)
	}
	if code := look("ops"); code != cli.ExitOK {
		t.Fatalf("an agent's look within the hour: exit %d", code)
	}
	if code := look("claude"); code != cli.ExitRefused {
		t.Fatalf("another hand rides on ops' Einblick: exit %d", code)
	}
	es, _ := journal.Read(env.Paths.Journal, time.Now().Add(-time.Hour))
	for _, e := range es {
		if strings.Contains(e.Detail, "4711") {
			t.Fatalf("the journal kept the family's text: %+v", e)
		}
	}
	// an hour later the Einblick has run out
	p, _ = proposal.Get(cli.InboxDir(env), p.ID)
	p.Decided = time.Now().Add(-envelope.GrantFor - time.Minute)
	proposal.Save(cli.InboxDir(env), p)
	if code := look("ops"); code != cli.ExitRefused {
		t.Fatalf("an agent's look after the hour: exit %d", code)
	}
	if code := look("human:test"); code != cli.ExitOK {
		t.Fatalf("the human's look: exit %d", code)
	}
}

// A laib.yml with another word for private does not load: a typo must not
// leave the family's data open.
func TestPrivateTakesOnlyItsTwoWords(t *testing.T) {
	env, _, _ := privateBox(t, "offen")
	if _, err := cli.LoadState(env); err == nil || !strings.Contains(err.Error(), "neither sealed nor approval") {
		t.Fatalf("private: offen loaded: %v", err)
	}
}

// A service that is not private stays as it was: looked into freely, its
// output in the journal.
func TestNotPrivateUnchanged(t *testing.T) {
	env, _, _ := privateBox(t, "approval")
	p := filepath.Join(env.Paths.State, "services", "whoami", "laib.yml")
	b, _ := os.ReadFile(p)
	os.WriteFile(p, []byte(strings.TrimPrefix(string(b), "private: approval\n")), 0o644)
	proposal.CommitDirty(context.Background(), env.Paths.State, "human:test")
	if code := envelope.Look(context.Background(), quiet(env, "ops"), []string{"whoami", "--", "cat", "/x"}); code != cli.ExitOK {
		t.Fatalf("look on a service that is not private: exit %d", code)
	}
	es, _ := journal.Read(env.Paths.Journal, time.Now().Add(-time.Hour))
	if len(es) == 0 || !strings.Contains(es[len(es)-1].Detail, "4711") {
		t.Fatalf("the journal of a service that is not private: %+v", es)
	}
}
