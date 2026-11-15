package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/sysd"
)

// digest is a sha256 digest of the right form, every hex digit c.
func digest(c string) string { return "sha256:" + strings.Repeat(c, 64) }

// cosignRefuses fakes the pinned cosign over testBox's host: the digests in
// bad carry no signature of our CI; it records what was checked.
func cosignRefuses(t *testing.T, bad map[string]bool) *[]string {
	t.Helper()
	inner := sysd.Run
	t.Cleanup(func() { sysd.Run = inner })
	var checked []string
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "podman" && len(args) > 0 && args[0] == "run" && strings.Contains(strings.Join(args, " "), "cosign") {
			img := args[len(args)-1]
			checked = append(checked, img)
			if _, d, _ := strings.Cut(img, "@"); bad[d] {
				return "Error: no matching signatures", errors.New("exit status 1")
			}
			return "The cosign claims were validated", nil
		}
		return inner(ctx, name, args...)
	}
	return &checked
}

// ADR 0034, decision 3: our own image (ghcr.io/mdopp/) is proposed only
// with our CI's signature. A new digest whose signature does not hold is
// refused for every proposer and nothing is kept; a signed one is a
// proposal a person taps, its notes on the card. A digest the box already
// runs passes without a check.
func TestAnUnsignedOwnImageIsRefused(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	const img = "ghcr.io/mdopp/homes:latest@"
	os.WriteFile(filepath.Join(env.Paths.State, "services", "whoami", "values.yml"), []byte("IMAGE: "+img+digest("a")+"\nTOKEN: !secret WHOAMI_TOKEN\n"), 0o644)
	if err := proposal.CommitDirty(ctx, env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	checked := cosignRefuses(t, map[string]bool{digest("b"): true})
	st, _ := cli.LoadState(env)
	out, code := mcp.Call(ctx, env, st, "ops", "laib_propose", map[string]any{"reason": "Neue Versionen: whoami", "sets": []any{"whoami/IMAGE=" + img + digest("b")}})
	if code == cli.ExitOK || !strings.Contains(out, "nicht von Laibs CI signiert") {
		t.Fatalf("an unsigned own image was proposed (exit %d): %s", code, out)
	}
	if ps, _ := proposal.List(cli.InboxDir(env), false); len(ps) != 0 {
		t.Fatalf("a refused proposal was kept: %+v", ps)
	}
	out, code = mcp.Call(ctx, env, st, "ops", "laib_propose", map[string]any{"reason": "Neue Versionen: whoami", "sets": []any{"whoami/IMAGE=" + img + digest("c")},
		"notes": []any{"whoami/IMAGE=Behebt eine Sicherheitslücke im Login.\nNeue Ansicht."}})
	if code != cli.ExitOK {
		t.Fatalf("a signed own image: exit %d: %s", code, out)
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	if len(open) != 1 || open[0].Class != "human" || open[0].Notes["whoami/IMAGE"] != "Behebt eine Sicherheitslücke im Login. Neue Ansicht." {
		t.Fatalf("want one open human-class proposal with its note on one line: %+v", open)
	}
	if len(*checked) != 2 || (*checked)[1] != "ghcr.io/mdopp/homes@"+digest("c") {
		t.Fatalf("the signatures checked: %v", *checked)
	}
}
