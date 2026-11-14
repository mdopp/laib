package diag

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/state"
)

// #1047: laib auslastung's values come from laib-disk load, the hardware
// program beside laib. The kernel hands it box.yml's dataDir, the GPU and the
// lease, shows its JSON as it said it, keeps it loadWait, and says so when
// laib-disk does not answer instead of showing nothing.
func TestAuslastungAsksLaibDisk(t *testing.T) {
	dir, stateDir := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(stateDir, "box.yml"), []byte("domain: box.example\ndataDir: /mnt/data/stacks\ngpu: {present: true}\nmodel: {lease: http://127.0.0.1:9091/api/model-lease}\n"), 0o644)
	os.WriteFile(filepath.Join(stateDir, "policy.yml"), []byte(state.DefaultPolicy), 0o644)
	bin := filepath.Join(dir, "laib-disk")
	os.WriteFile(bin, []byte(`#!/bin/sh
echo "$@" >> "$0.args"
echo '{"at":"2026-11-14T09:00:00Z","cpu":{"cores":4}}'
`), 0o755)
	t.Setenv("LAIB_DISK", bin)
	loadLast = nil
	defer func() { loadLast = nil }()
	ask := func() (int, string) {
		var out, errw bytes.Buffer
		env := &cli.Env{JSON: true, Out: &out, ErrOut: &errw}
		env.Paths.State = stateDir
		code := LoadCmd(context.Background(), env, nil)
		return code, out.String() + errw.String()
	}
	if code, out := ask(); code != 0 || !strings.Contains(out, `"cores": 4`) || !strings.Contains(out, `"at": "2026-11-14T09:00:00Z"`) {
		t.Fatalf("exit %d: %s", code, out)
	}
	ask() // inside loadWait: kept, laib-disk is not asked again
	args, _ := os.ReadFile(bin + ".args")
	if string(args) != "load --data-dir /mnt/data/stacks --gpu --lease http://127.0.0.1:9091/api/model-lease\n" {
		t.Errorf("laib-disk was asked %q", args)
	}
	loadLast = nil
	t.Setenv("LAIB_DISK", filepath.Join(dir, "missing"))
	if code, out := ask(); code == 0 || !strings.Contains(out, "laib-disk antwortet nicht") {
		t.Fatalf("a missing laib-disk is not said: exit %d %s", code, out)
	}
}
