package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/registry"
)

// #906: a template fix on the registry's main reaches the box as a proposal
// without a release and without waiting for the weekly timer: `laib
// template update <svc>` syncs the box.yml registries first, and the
// registry's template (its folder at registries[].path) is what it diffs.
func TestTemplateUpdateSyncsTheRegistryFirst(t *testing.T) {
	env, _ := testBox(t)
	repo := t.TempDir()
	tpl := filepath.Join(repo, "vorlagen", "echo")
	os.MkdirAll(tpl, 0o755)
	os.WriteFile(filepath.Join(tpl, "laib.yml"), []byte("name: echo\ntemplate: {registry: laib, name: echo, version: 2}\nhealth:\n  none: a stub\n"), 0o644)
	os.WriteFile(filepath.Join(tpl, "echo.container"), []byte("[Container]\nImage=y\n"), 0o644)
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "t"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	dir := filepath.Join(env.Paths.State, "services", "echo")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "laib.yml"), []byte("name: echo\ntemplate: {registry: laib, name: echo, version: 1}\nhealth:\n  none: a stub\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "echo.container"), []byte("[Container]\nImage=x\n"), 0o644)
	f, _ := os.OpenFile(filepath.Join(env.Paths.State, "box.yml"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("registries:\n  - {name: laib, url: " + repo + ", ref: main, path: vorlagen}\n")
	f.Close()
	if err := proposal.CommitDirty(context.Background(), env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	if code := registry.TemplateCmd(context.Background(), env, []string{"update", "echo"}); code != cli.ExitOK {
		t.Fatalf("template update echo: exit %d", code)
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	for _, p := range open {
		if p.Reason == "template update echo" {
			return
		}
	}
	t.Fatalf("no proposal from the registry's echo: %v", open)
}
