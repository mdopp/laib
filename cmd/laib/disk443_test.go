package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #443: `laib disk` stays the human's verb (tier apply, humans only) and
// hands the work to laib-disk, with the actor and --json; its output and
// exit code are laib disk's.
func TestLaibDiskForwardsToTheProgram(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "laib-disk")
	os.WriteFile(bin, []byte("#!/bin/sh\necho \"actor=$LAIB_ACTOR args=$*\"\nexit 3\n"), 0o755)
	t.Setenv("LAIB_DISK", bin)
	var out strings.Builder
	env := &cliEnv{}
	env.Actor, env.JSON, env.Out = "human:core", true, &out
	if code := cmdDisk(t.Context(), env, []string{"add", "/dev/sdb"}); code != 3 {
		t.Errorf("exit %d, want laib-disk's 3", code)
	}
	if got := out.String(); got != "actor=human:core args=--json add /dev/sdb\n" {
		t.Errorf("laib-disk got %q", got)
	}
}
