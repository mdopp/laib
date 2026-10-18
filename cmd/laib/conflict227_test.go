package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
)

// A refused apply writes nothing (#227 review): `laib add` with ServiceBay's
// file still in the Quadlet root must not generate the service's missing
// secrets into secrets.age (or mint its tokens) before it refuses, so the
// values carried over afterwards (#228) are the only ones there ever were.
func TestApplyRefusedForAConflictLeavesSecretsAlone(t *testing.T) {
	c := newCbox(t)
	dir := filepath.Join(c.env.Paths.State, "services", "tick")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "laib.yml"), []byte("name: tick\ntemplate: {registry: vm, name: tick, version: 1}\nroutes: []\nsecrets:\n  - name: TICK_TOKEN\ndata: []\nhealth: {none: a test unit}\nports: []\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "tick.container"), []byte("[Container]\nImage=docker.io/library/alpine:3\nEnvironment=TOKEN=${TOKEN}\n\n[Install]\nWantedBy=default.target\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "values.yml"), []byte("TOKEN: !secret TICK_TOKEN\n"), 0o644)
	os.WriteFile(filepath.Join(c.env.Paths.Quadlet, "tick.kube"), []byte("[Kube]\nYaml=tick.yml\n"), 0o644)
	before, _ := os.ReadFile(c.env.Paths.Secrets)
	st, err := cli.LoadState(c.env)
	if err != nil {
		t.Fatal(err)
	}
	c.calls = nil
	if rc := apply.ApplyLocked(context.Background(), c.env, st, "", "test: add beside ServiceBay's tick.kube"); rc == cli.ExitOK {
		t.Fatal("apply did not refuse while tick.kube is in the Quadlet root")
	}
	if after, _ := os.ReadFile(c.env.Paths.Secrets); !bytes.Equal(before, after) {
		t.Error("the refused apply rewrote secrets.age")
	}
	if entries, _ := os.ReadDir(filepath.Join(c.env.Paths.Quadlet, "tick")); len(entries) > 0 {
		t.Errorf("the refused apply wrote units: %v", entries)
	}
}
