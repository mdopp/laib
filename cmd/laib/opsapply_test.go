package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
)

// opsBox is a box whose values changes wait for an approval, with a
// restic seam that records the snapshots taken.
func opsBox(t *testing.T) (*cbox, *[]string) {
	t.Helper()
	c := newCbox(t)
	p := filepath.Join(c.env.paths.State, "policy.yml")
	b, _ := os.ReadFile(p)
	text := strings.Replace(strings.Replace(string(b), "values: auto", "values: proposal", 1), "  apply: {by: human}\n", "", 1)
	os.WriteFile(p, []byte(text), 0o644)
	proposal.CommitDirty(context.Background(), c.env.paths.State, "human:test")
	sec, _ := secrets.Load(c.env.paths.Secrets, c.env.paths.Key)
	sec[resticPassword] = "pw"
	secrets.Save(c.env.paths.Secrets, c.env.paths.Key, sec)
	var snaps []string
	real := resticRun
	resticRun = func(_ context.Context, _ *state.State, _ string, args ...string) (string, error) {
		snaps = append(snaps, strings.Join(args, " "))
		return "", nil
	}
	t.Cleanup(func() { resticRun = real })
	return c, &snaps
}

func opsPropose(t *testing.T, c *cbox, image string) int {
	t.Helper()
	c.env.actor = "ops"
	if rc := cmdPropose(context.Background(), c.env, []string{"--set", "services/whoami/values.yml:IMAGE=" + image, "neueres whoami"}); rc != exitOK {
		t.Fatalf("propose rc %d", rc)
	}
	ps, _ := proposal.List(inboxDir(c.env), true)
	return ps[len(ps)-1].ID
}

// Owner decision on #286 (2026-10-17): a capable model may apply a small
// fix itself — its own proposal, inside the envelope: the hourly budget, a
// snapshot first, verify, back on red. The kernel enforces it.
func TestTheOperatorAppliesItsOwnFixInsideTheEnvelope(t *testing.T) {
	c, snaps := opsBox(t)
	id := opsPropose(t, c, "docker.io/traefik/whoami:v1.11")
	if rc := cmdApply(context.Background(), c.env, []string{"--proposal", fmt.Sprint(id)}); rc != exitOK {
		t.Fatalf("apply rc %d", rc)
	}
	p, _ := proposal.Get(inboxDir(c.env), id)
	if p.Status != "approved" {
		t.Fatalf("proposal %s", p.Status)
	}
	if len(*snaps) == 0 || !strings.Contains((*snaps)[0], "pre-repair") {
		t.Fatalf("no snapshot before the apply: %v", *snaps)
	}
	es, _ := journal.Read(c.env.paths.Journal, time.Now().Add(-time.Hour))
	booked := false
	for _, e := range es {
		booked = booked || (e.Actor == "ops" && e.Verb == "apply" && e.Service == "whoami")
	}
	if !booked {
		t.Fatal("the apply is not booked against whoami's budget")
	}
}

// Outside the envelope the kernel refuses: no proposal named, a spent
// budget, a proposal only a human may free.
func TestTheOperatorsApplyIsRefusedOutsideTheEnvelope(t *testing.T) {
	c, _ := opsBox(t)
	c.env.actor = "ops"
	if rc := cmdApply(context.Background(), c.env, []string{"whoami"}); rc != exitRefused {
		t.Fatalf("the Operator applied without a proposal: rc %d", rc)
	}
	id := opsPropose(t, c, "docker.io/traefik/whoami:v1.12")
	for range RepairBudget {
		journal.Append(c.env.paths.Journal, journal.Entry{Actor: "ops", Verb: "restart", Service: "whoami", Result: "green"})
	}
	if rc := cmdApply(context.Background(), c.env, []string{"--proposal", fmt.Sprint(id)}); rc != exitRefused {
		t.Fatalf("applied with the budget spent: rc %d", rc)
	}
	if p, _ := proposal.Get(inboxDir(c.env), id); p.Status != "open" {
		t.Fatalf("the refused proposal is %s", p.Status)
	}
}

func TestTheOperatorNeverAppliesWhatOnlyAHumanMayFree(t *testing.T) {
	c, _ := opsBox(t)
	c.env.actor = "ops"
	patch := "--- /dev/null\n+++ b/services/whoami/post-deploy\n@@ -0,0 +1,2 @@\n+#!/bin/sh\n+echo hi\n"
	c.env.in = strings.NewReader(patch)
	if rc := cmdPropose(context.Background(), c.env, []string{"--patch", "whoami braucht ein Skript"}); rc != exitOK {
		t.Fatalf("propose rc %d", rc)
	}
	ps, _ := proposal.List(inboxDir(c.env), true)
	id := ps[len(ps)-1].ID
	if rc := cmdApply(context.Background(), c.env, []string{"--proposal", fmt.Sprint(id)}); rc != exitRefused {
		t.Fatalf("the Operator freed a human-class proposal: rc %d", rc)
	}
}
