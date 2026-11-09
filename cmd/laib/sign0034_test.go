package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
)

// ADR 0034, decision 3: our own image (ghcr.io/mdopp/) is proposed only
// with our CI's signature. A new digest whose signature does not hold is
// refused for every proposer and nothing is kept; a signed one is a
// proposal a person taps, its notes on the card. A digest the box already
// runs passes without a check.
func TestAnUnsignedOwnImageIsRefused(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	const img = "ghcr.io/mdopp/homes:latest@sha256:"
	os.WriteFile(filepath.Join(env.Paths.State, "services", "whoami", "values.yml"), []byte("IMAGE: "+img+"a1\nTOKEN: !secret WHOAMI_TOKEN\n"), 0o644)
	if err := proposal.CommitDirty(ctx, env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	real := inbox.VerifyImage
	t.Cleanup(func() { inbox.VerifyImage = real })
	var checked []string
	inbox.VerifyImage = func(image string) string {
		checked = append(checked, image)
		if strings.HasSuffix(image, "@sha256:bad") {
			return "keine gültige Signatur aus Laibs CI: no matching signatures"
		}
		return ""
	}
	st, _ := cli.LoadState(env)
	out, code := mcp.Call(ctx, env, st, "ops", "laib_propose", map[string]any{"reason": "Neue Versionen: whoami", "sets": []any{"whoami/IMAGE=" + img + "bad"}})
	if code == cli.ExitOK || !strings.Contains(out, "nicht von Laibs CI signiert") {
		t.Fatalf("an unsigned own image was proposed (exit %d): %s", code, out)
	}
	if ps, _ := proposal.List(cli.InboxDir(env), false); len(ps) != 0 {
		t.Fatalf("a refused proposal was kept: %+v", ps)
	}
	out, code = mcp.Call(ctx, env, st, "ops", "laib_propose", map[string]any{"reason": "Neue Versionen: whoami", "sets": []any{"whoami/IMAGE=" + img + "b2"},
		"notes": []any{"whoami/IMAGE=Behebt eine Sicherheitslücke im Login.\nNeue Ansicht."}})
	if code != cli.ExitOK {
		t.Fatalf("a signed own image: exit %d: %s", code, out)
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	if len(open) != 1 || open[0].Class != "human" || open[0].Notes["whoami/IMAGE"] != "Behebt eine Sicherheitslücke im Login. Neue Ansicht." {
		t.Fatalf("want one open human-class proposal with its note on one line: %+v", open)
	}
	if len(checked) != 2 || checked[1] != img+"b2" {
		t.Fatalf("the signatures checked: %v", checked)
	}
}
