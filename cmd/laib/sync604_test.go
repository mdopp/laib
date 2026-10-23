package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/registry"
)

// The template sync runs after every update now, not only on Saturdays:
// a service whose template update is still open in the Inbox gets no second
// proposal for it, however often the sync runs.
func TestTemplateSyncProposesOneOpenUpdatePerService(t *testing.T) {
	env, _ := testBox(t)
	env.Actor = "laib"
	tpl := filepath.Join(env.Paths.Home, "registries", "vm", "templates", "echo")
	os.MkdirAll(tpl, 0o755)
	os.WriteFile(filepath.Join(tpl, "laib.yml"), []byte("name: echo\ntemplate: {registry: vm, name: echo, version: 2}\nhealth:\n  none: a stub\n"), 0o644)
	os.WriteFile(filepath.Join(tpl, "echo.container"), []byte("[Container]\nImage=y\n"), 0o644)
	dir := filepath.Join(env.Paths.State, "services", "echo")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "laib.yml"), []byte("name: echo\ntemplate: {registry: vm, name: echo, version: 1}\nhealth:\n  none: a stub\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "echo.container"), []byte("[Container]\nImage=x\n"), 0o644)
	if err := proposal.CommitDirty(context.Background(), env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		st, err := cli.LoadState(env)
		if err != nil {
			t.Fatal(err)
		}
		code := registry.Update(context.Background(), env, st, "echo")
		t.Logf("exit %d", code)
		if code != cli.ExitOK && code != cli.ExitUsage {
			t.Fatalf("exit %d", code)
		}
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	n := 0
	for _, p := range open {
		if p.Reason == "template update echo" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d open template updates for echo, want 1", n)
	}
}
