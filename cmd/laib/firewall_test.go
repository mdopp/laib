package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/pages"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// fwBox: a loaded state, a render dir with (or without) the rendered
// ruleset, and a sysd shim that answers `nft list table inet laib` as told.
func fwBox(t *testing.T, rendered bool, loaded bool) (*cliEnv, *state.State, *[]string) {
	t.Helper()
	st, err := state.Load(filepath.Join("..", "..", "testdata", "golden", "basic", "state"))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	paths := boxfs.Paths{Home: home, State: filepath.Join(home, "state"), Journal: filepath.Join(home, "journal"), Render: filepath.Join(home, "render"), Lock: filepath.Join(home, "apply.lock")}
	if rendered {
		os.MkdirAll(filepath.Join(paths.Render, "nftables"), 0o755)
		os.WriteFile(filepath.Join(paths.Render, "nftables", "laib.nft"), []byte("table inet laib {}\n"), 0o644)
	}
	var calls []string
	real := sysd.Run
	t.Cleanup(func() { sysd.Run = real })
	sysd.Run = func(_ context.Context, name string, args ...string) (string, error) {
		line := name + " " + strings.Join(args, " ")
		calls = append(calls, line)
		switch {
		case strings.Contains(line, "nft list table inet laib"):
			if loaded {
				return "table inet laib {\n}", nil
			}
			return "Error: No such file or directory", errors.New("exit status 1")
		case strings.Contains(line, "is-active"):
			return "active", nil
		}
		return "", nil
	}
	return &cliEnv{Paths: paths, Actor: "human:test"}, st, &calls
}

// The ruleset lives in kernel memory and the rendered file never changes
// across a reboot: readiness must look at the kernel, not at the file (#37).
func TestReadinessIsRestrictedWhenTheFirewallTableIsMissing(t *testing.T) {
	env, st, _ := fwBox(t, true, false)
	r, _ := pages.CollectStatus(context.Background(), env, st)
	if r.State != "eingeschränkt" || !strings.Contains(r.Lock, "firewall") || r.Firewall != "fehlt" {
		t.Fatalf("a rendered ruleset that is not in the kernel must restrict readiness: %+v", r)
	}
	env, st, _ = fwBox(t, true, true)
	r, _ = pages.CollectStatus(context.Background(), env, st)
	if r.State != "bereit" || r.Firewall != "geladen" {
		t.Fatalf("loaded ruleset: %+v", r)
	}
	env, st, _ = fwBox(t, false, false)
	r, _ = pages.CollectStatus(context.Background(), env, st)
	if r.State != "bereit" || r.Firewall != "" {
		t.Fatalf("no ruleset rendered, nothing to load: %+v", r)
	}
	if r.Version != Version {
		t.Fatalf("/ready must carry the running version (the update path checks it): %+v", r)
	}
}

// Boot-time convergence of the one piece of non-file state: serve and
// converge load the rendered ruleset when the kernel has no table.
func TestEnsureFirewallLoadsTheRulesetOnlyWhenMissing(t *testing.T) {
	env, _, calls := fwBox(t, true, false)
	loaded, err := apply.EnsureFirewall(context.Background(), env.Paths)
	if err != nil || !loaded {
		t.Fatalf("loaded=%v err=%v", loaded, err)
	}
	if !strings.Contains(strings.Join(*calls, "\n"), "sudo nft -f "+filepath.Join(env.Paths.Render, "nftables", "laib.nft")) {
		t.Fatalf("nft -f not run: %v", *calls)
	}
	env, _, calls = fwBox(t, true, true)
	if loaded, _ := apply.EnsureFirewall(context.Background(), env.Paths); loaded {
		t.Fatal("the table is there: nothing to load")
	}
	for _, c := range *calls {
		if strings.Contains(c, "nft -f") {
			t.Fatal("nft -f run although the table is loaded")
		}
	}
	env, _, calls = fwBox(t, false, false)
	if loaded, err := apply.EnsureFirewall(context.Background(), env.Paths); loaded || err != nil {
		t.Fatalf("no ruleset rendered: loaded=%v err=%v", loaded, err)
	}
}

// `laib self rollback` puts the previous binaries back and restarts what
// runs them; the install script keeps them as *.prev (#37).
func TestSelfRollbackSwapsThePreviousBinariesAndRestarts(t *testing.T) {
	env, _, calls := fwBox(t, false, true)
	dir := t.TempDir()
	for _, b := range []string{"laib", "laib-operator", "laib-lease", "laib-haushalt"} {
		os.WriteFile(filepath.Join(dir, b), []byte("new"), 0o755)
		os.WriteFile(filepath.Join(dir, b+".prev"), []byte("old"), 0o755)
	}
	t.Setenv("LAIB_BIN_DIR", dir)
	if rc := cmdSelf(context.Background(), env, []string{"rollback"}); rc != cli.ExitOK {
		t.Fatalf("rc %d", rc)
	}
	joined := strings.Join(*calls, "\n")
	for _, want := range []string{"sudo mv " + filepath.Join(dir, "laib.prev") + " " + filepath.Join(dir, "laib"), "sudo mv " + filepath.Join(dir, "laib-lease.prev"), "sudo mv " + filepath.Join(dir, "laib-haushalt.prev"), "systemctl --user try-restart laib.service"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in\n%s", want, joined)
		}
	}
	if rc := cmdSelf(context.Background(), &cliEnv{Paths: env.Paths, Actor: "ops"}, []string{"rollback"}); rc == cli.ExitOK {
		t.Fatal("an agent rolled the binaries back")
	}
	os.Remove(filepath.Join(dir, "laib.prev"))
	if rc := cmdSelf(context.Background(), env, []string{"rollback"}); rc == cli.ExitOK {
		t.Fatal("rollback without a previous binary must refuse")
	}
}
