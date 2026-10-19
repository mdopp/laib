package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/journal"
)

// The owner's mail itself (#393) moved with laib-notify (#443): notify/notify_test.go.

// #393: a failed or stale nightly backup is red in diag, so it reaches the owner's mail.
func TestAFailedOrStaleBackupIsRed(t *testing.T) {
	b := newRbox(t)
	st, err := cli.LoadState(b.env)
	if err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(b.env.Paths.Journal)
	journal.Append(b.env.Paths.Journal, journal.Entry{T: time.Now().Add(-72 * time.Hour), Actor: "human", Verb: "init", Result: "green"})
	stale := func() bool {
		for _, f := range diag.Probes(context.Background(), b.env, st, "") {
			if f.Probe == "backup" && strings.Contains(f.Detail, "letzte Sicherung") {
				return f.Severity == "rot"
			}
		}
		return false
	}
	if !stale() {
		t.Fatal("three days without a backup must be red")
	}
	journal.Append(b.env.Paths.Journal, journal.Entry{Actor: "laib", Verb: "backup", Result: "green"})
	if stale() {
		t.Fatal("a fresh green backup is no finding")
	}
	journal.Append(b.env.Paths.Journal, journal.Entry{Actor: "laib", Verb: "backup", Result: "red"})
	if !stale() {
		t.Fatal("a failed last backup must be red")
	}
}
