package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/secrets"
)

// #576: an agent writes `laib propose --patch` against the state repo's real
// text, so a read verb lists its files and prints one as committed on main —
// never secrets.age, and never a secret's value, by value or by key.
func TestStateShowsTheRepoAsOnMainWithSecretsRedacted(t *testing.T) {
	env, _ := testBox(t)
	ctx := context.Background()
	const val = "zz-test-secret-value-zz"
	if err := secrets.Save(env.Paths.Secrets, env.Paths.Key, map[string]string{"WHOAMI_TOKEN": val}); err != nil {
		t.Fatal(err)
	}
	svc := filepath.Join(env.Paths.State, "services", "whoami")
	os.WriteFile(filepath.Join(svc, "values.yml"), []byte("IMAGE: docker.io/traefik/whoami:v1.10\nTOKEN: !secret WHOAMI_TOKEN\nWHOAMI_TOKEN: a-literal-by-mistake\n"), 0o644)
	os.WriteFile(filepath.Join(svc, "notes.yml"), []byte("copied: "+val+"\n"), 0o644)
	if err := proposal.CommitDirty(ctx, env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	// an uncommitted edit is not main
	boxYml, _ := os.ReadFile(filepath.Join(env.Paths.State, "box.yml"))
	os.WriteFile(filepath.Join(env.Paths.State, "box.yml"), append(boxYml, "# not-committed\n"...), 0o644)

	run := func(args ...string) (string, string, int) {
		var out, errOut bytes.Buffer
		e := *env
		e.Out, e.ErrOut, e.Actor = &out, &errOut, "claude"
		code := cmdState(ctx, &e, args)
		return out.String(), errOut.String(), code
	}

	list, _, code := run()
	if code != cli.ExitOK {
		t.Fatalf("list: exit %d", code)
	}
	for _, f := range []string{"box.yml", "policy.yml", "services/whoami/values.yml", "services/whoami/laib.yml"} {
		if !slices.Contains(strings.Split(list, "\n"), f) {
			t.Errorf("list lacks %s:\n%s", f, list)
		}
	}
	if strings.Contains(list, "secrets.age") {
		t.Errorf("list shows secrets.age:\n%s", list)
	}

	box, _, _ := run("box.yml")
	if strings.Contains(box, "not-committed") || !strings.Contains(box, "box.test") {
		t.Errorf("box.yml is not main's:\n%s", box)
	}

	values, errOut, code := run("services/whoami/values.yml")
	if code != cli.ExitOK || strings.Contains(values, "a-literal-by-mistake") || !strings.Contains(values, "WHOAMI_TOKEN: ${secret:WHOAMI_TOKEN}") || !strings.Contains(values, "TOKEN: !secret WHOAMI_TOKEN") {
		t.Errorf("values.yml (exit %d):\n%s", code, values)
	}
	if !strings.Contains(errOut, "WHOAMI_TOKEN") {
		t.Errorf("the output does not say what was redacted: %q", errOut)
	}
	if notes, _, _ := run("services/whoami/notes.yml"); strings.Contains(notes, val) || !strings.Contains(notes, "${secret:WHOAMI_TOKEN}") {
		t.Errorf("a secret's value is shown:\n%s", notes)
	}

	for _, bad := range []string{"secrets.age", "../age.key", "services/../../age.key", "nope.yml"} {
		if out, _, code := run(bad); code == cli.ExitOK || strings.Contains(out, val) {
			t.Errorf("state %s: exit %d, %q", bad, code, out)
		}
	}

	var j struct {
		Path     string   `json:"path"`
		Content  string   `json:"content"`
		Redacted []string `json:"redacted"`
	}
	var out bytes.Buffer
	e := *env
	e.Out, e.ErrOut, e.JSON = &out, &bytes.Buffer{}, true
	if cmdState(ctx, &e, []string{"services/whoami/values.yml"}) != cli.ExitOK || json.Unmarshal(out.Bytes(), &j) != nil ||
		!slices.Contains(j.Redacted, "WHOAMI_TOKEN") || strings.Contains(j.Content, "a-literal") {
		t.Errorf("json: %s", out.String())
	}

	// the default hands reach it by their ceiling, without a policy edit
	st, err := cli.LoadState(env)
	if err != nil {
		t.Fatal(err)
	}
	for _, hand := range []string{"claude", "pi", "ops", "bote"} {
		var names []string
		for _, tl := range mcp.Tools(st, hand) {
			names = append(names, tl["name"].(string))
		}
		if !slices.Contains(names, "laib_state") {
			t.Errorf("%s does not see laib_state", hand)
		}
	}
	if got, code := mcp.Call(ctx, env, st, "claude", "laib_state", map[string]any{"path": "services/whoami/values.yml"}); code != cli.ExitOK || strings.Contains(got, "a-literal") || !strings.Contains(got, "WHOAMI_TOKEN") {
		t.Errorf("laib_state over MCP: exit %d\n%s", code, got)
	}
}
