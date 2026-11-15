package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
)

// fakeUpdater is laib-updater behind the door, answering laib updates with
// rows; it records what the kernel asked.
func fakeUpdater(t *testing.T, rows *[]map[string]string) *[]map[string]any {
	t.Helper()
	var asked []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var c map[string]any
		json.NewDecoder(r.Body).Decode(&c)
		asked = append(asked, c)
		b, _ := json.Marshal(*rows)
		json.NewEncoder(w).Encode(map[string]any{"out": string(b), "code": 0})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LAIB_UPDATER_URL", srv.URL)
	return &asked
}

// Ablauf L-57. ADR 0035 (#1233): the kernel reads no answer from the
// internet — laib-updater asks the registries — and never trusts what it
// says. A digest of the wrong form is refused when it is proposed; our own
// image's signature is checked by the kernel when it is proposed and again
// right before the run that would apply it: a signature that does not hold
// then takes the approval back before anything runs, its card says why in
// words, and the box stays on what it runs.
func TestTheKernelRefusesADigestOrSignatureTheUpdaterGotWrong(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	const homes = "ghcr.io/mdopp/homes:latest@"
	os.WriteFile(filepath.Join(env.Paths.State, "services", "whoami", "values.yml"), []byte("IMAGE: "+homes+digest("a")+"\nTOKEN: !secret WHOAMI_TOKEN\n"), 0o644)
	if err := proposal.CommitDirty(ctx, env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	bad := map[string]bool{}
	cosignRefuses(t, bad)
	// the updater says a new digest is there, and that it is signed
	rows := []map[string]string{{"service": "whoami", "key": "IMAGE", "image": "ghcr.io/mdopp/homes", "current": "latest", "digest": "sha256:zz"}}
	asked := fakeUpdater(t, &rows)
	st, _ := cli.LoadState(env)
	out, code := mcp.Call(ctx, env, st, "ops", "laib_updates", map[string]any{"notes": true})
	if code != cli.ExitOK || !strings.Contains(out, `"digest": "sha256:zz"`) || len(*asked) != 1 || fmt.Sprint((*asked)[0]["argv"]) != "[updates --notes]" {
		t.Fatalf("laib updates shows the updater's rows (exit %d): %s; asked %v", code, out, *asked)
	}
	set := func(d string) (string, int) {
		return mcp.Call(ctx, env, st, "ops", "laib_propose", map[string]any{"reason": "Neue Versionen: whoami", "sets": []any{"whoami/IMAGE=" + homes + d}})
	}
	if out, code := set("sha256:zz"); code == cli.ExitOK || !strings.Contains(out, "kein Image mit gültigem Digest") {
		t.Fatalf("a digest of the wrong form was proposed (exit %d): %s", code, out)
	}
	if out, code := set(digest("b")); code != cli.ExitOK {
		t.Fatalf("a signed own image: exit %d: %s", code, out)
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	if len(open) != 1 {
		t.Fatalf("want one open proposal: %+v", open)
	}
	bad[digest("b")] = true // by the run, its signature no longer holds
	inbox.Approve(ctx, &cliEnv{Paths: env.Paths, Actor: "human:test", Out: io.Discard, ErrOut: io.Discard}, []string{fmt.Sprint(open[0].ID)})
	p, _ := proposal.Get(cli.InboxDir(env), open[0].ID)
	if p.Outcome != "rolled-back" || !strings.Contains(p.Because, "vor dem Einspielen geprüft") || !strings.Contains(p.Because, "nicht von Laibs CI signiert") {
		t.Fatalf("the approval is not taken back with its reason: outcome %q, because %q", p.Outcome, p.Because)
	}
	if b, _ := os.ReadFile(filepath.Join(env.Paths.State, "services", "whoami", "values.yml")); !strings.Contains(string(b), digest("a")) {
		t.Fatalf("the state did not go back to what runs: %s", b)
	}
	if b, err := os.ReadFile(filepath.Join(env.Paths.Quadlet, "whoami", "whoami.container")); err == nil && strings.Contains(string(b), digest("b")) {
		t.Fatalf("the unsigned image reached the unit:\n%s", b)
	}
}
