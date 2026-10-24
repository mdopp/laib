package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
)

// ADR 0027 (#624): laib self rollback no longer swaps laib-operator and
// laib-notify as binaries — a release carries none. It puts the previous
// laib back and lets it converge: the Operator's and notify's images are
// pinned by the laib that renders them, so they go back with it.
func TestSelfRollbackLeavesTheContainersToThePreviousLaib(t *testing.T) {
	c := newCbox(t)
	dir := t.TempDir()
	t.Setenv("LAIB_BIN_DIR", dir)
	for _, b := range []string{"laib", "laib.prev", "laib-lease", "laib-lease.prev", "laib-operator"} { // an old host binary, no .prev
		os.WriteFile(filepath.Join(dir, b), []byte("#!/bin/sh\n"), 0o755)
	}
	c.calls = nil
	if rc := cmdSelf(context.Background(), c.env, []string{"rollback"}); rc != cli.ExitOK {
		t.Fatalf("rollback rc %d: a box with no laib-operator.prev must roll back\n%s", rc, strings.Join(c.calls, "\n"))
	}
	joined := strings.Join(c.calls, "\n")
	if strings.Contains(joined, "laib-operator") || strings.Contains(joined, "laib-notify") {
		t.Errorf("the Operator or notify were treated as binaries or host units:\n%s", joined)
	}
	if !strings.Contains(joined, "sudo mv "+filepath.Join(dir, "laib.prev")+" "+filepath.Join(dir, "laib")) || !strings.Contains(joined, "sudo mv "+filepath.Join(dir, "laib-lease.prev")) {
		t.Errorf("laib and laib-lease are not put back:\n%s", joined)
	}
	if !strings.Contains(joined, "LAIB_ACTOR=laib "+filepath.Join(dir, "laib")+" converge --force") {
		t.Errorf("the previous laib does not converge the box to its own rendering:\n%s", joined)
	}
}
