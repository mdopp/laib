package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/proposal"
)

// #959: Freigabe #297 told Signal „Home Assistant bekommt eine neue Version:
// 407b89cd…98f → 23f4dfbb…972e133“. A proposal that moves an image's digest
// reads both digests' version labels when it is made and keeps them, so
// the card — and the Signal line, its summary — says the version.
func TestADigestProposalCarriesTheVersions(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	const img = "docker.io/traefik/whoami:latest@sha256:"
	os.WriteFile(filepath.Join(env.Paths.State, "services", "whoami", "values.yml"), []byte("IMAGE: "+img+"a1\nTOKEN: !secret WHOAMI_TOKEN\n"), 0o644)
	if err := proposal.CommitDirty(ctx, env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	inbox.ImageVersion = func(image string) string { return map[string]string{img + "a1": "1.10.3", img + "b2": "1.10.4"}[image] }
	if code := inbox.Propose(ctx, &cliEnv{Paths: env.Paths, Actor: "human:other"}, []string{"--set", "services/whoami/values.yml:IMAGE=" + img + "b2", "neues Image"}); code != cli.ExitOK {
		t.Fatalf("propose: %d", code)
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	c := proposal.Card(open[0], "box.test")
	if !strings.HasSuffix(c.Summary, "bekommt ein Update: 1.10.3 → 1.10.4") || strings.Contains(c.Summary, "a1") {
		t.Errorf("the headline is not the versions: %q (changes %+v)", c.Summary, open[0].Changes)
	}
}
