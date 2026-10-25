package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
)

// ADR 0027 (#624): laib self rollback no longer swaps laib-operator,
// laib-notify, laib-haushalt or laib-lease as binaries — a release carries
// none. It puts the previous laib back and lets it converge: their images
// are pinned by the laib that renders them (the lease's through the llama
// template's unit), so they go back with it.
func TestSelfRollbackLeavesTheContainersToThePreviousLaib(t *testing.T) {
	c := newCbox(t)
	dir := t.TempDir()
	t.Setenv("LAIB_BIN_DIR", dir)
	for _, b := range []string{"laib", "laib.prev", "laib-lease", "laib-lease.prev", "laib-haushalt", "laib-haushalt.prev", "laib-operator"} { // old host binaries
		os.WriteFile(filepath.Join(dir, b), []byte("#!/bin/sh\n"), 0o755)
	}
	c.calls = nil
	if rc := cmdSelf(context.Background(), c.env, []string{"rollback"}); rc != cli.ExitOK {
		t.Fatalf("rollback rc %d: a box with no laib-operator.prev must roll back\n%s", rc, strings.Join(c.calls, "\n"))
	}
	joined := strings.Join(c.calls, "\n")
	for _, p := range []string{"laib-operator", "laib-notify", "laib-haushalt", "laib-lease"} {
		if strings.Contains(joined, p) {
			t.Errorf("%s was treated as a binary or a host unit:\n%s", p, joined)
		}
	}
	if !strings.Contains(joined, "sudo mv "+filepath.Join(dir, "laib.prev")+" "+filepath.Join(dir, "laib")) {
		t.Errorf("laib is not put back:\n%s", joined)
	}
	if !strings.Contains(joined, "LAIB_ACTOR=laib "+filepath.Join(dir, "laib")+" converge --force") {
		t.Errorf("the previous laib does not converge the box to its own rendering:\n%s", joined)
	}
}
