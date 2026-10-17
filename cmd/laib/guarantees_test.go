package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/pages"
	"github.com/mdopp/laib/internal/proposal"
)

// haus#2: the three guarantees the family sees come from readiness —
// the last successful backup (when, how many places) and the last applied
// change in plain words, with whether it can be taken back.
func TestReadinessCarriesTheLastBackupAndTheLastChange(t *testing.T) {
	env, _ := testBox(t)
	st, err := cli.LoadState(env)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := pages.CollectStatus(context.Background(), env, st)
	if r.LastBackup.OK || r.LastBackup.At != "" {
		t.Fatalf("no backup yet must say so: %+v", r.LastBackup)
	}
	if r.LastChange.Summary != "" {
		t.Fatalf("no change yet: %+v", r.LastChange)
	}
	when := time.Now().Add(-2 * time.Hour).UTC()
	journal.Append(env.Paths.Journal, journal.Entry{T: when, Actor: "laib", Verb: "backup", Result: "green", Detail: "3 Pfade"})
	inbox := cli.InboxDir(env)
	os.MkdirAll(inbox, 0o755)
	os.WriteFile(inbox+"/4.json", []byte(`{"id":4,"actor":"ops","reason":"Dokumente scannen","branch":"proposal/4","tier":"apply","status":"approved","created":"2026-10-11T10:00:00Z","decided":"2026-10-11T18:40:00Z","decidedBy":"human:core","files":["services/paperless/laib.yml"],"class":"new-service","kind":"new-service","adds":["paperless"],"facts":["paperless ist unter paperless.box.test nur im Heimnetz erreichbar"]}`), 0o644)
	r, _ = pages.CollectStatus(context.Background(), env, st)
	if !r.LastBackup.OK || r.LastBackup.Repos != 1 || r.LastBackup.At == "" {
		t.Fatalf("last backup: %+v", r.LastBackup)
	}
	if r.LastChange.Proposal != 4 || r.LastChange.Summary == "" || !r.LastChange.Reversible || r.LastChange.At == "" {
		t.Fatalf("last change: %+v", r.LastChange)
	}
	c := proposal.Card(&proposal.Proposal{ID: 4, Adds: []string{"paperless"}, Facts: []string{"paperless ist unter paperless.box.test nur im Heimnetz erreichbar"}}, "box.test")
	if r.LastChange.Summary != c.Summary {
		t.Fatalf("the summary is the card's: %q vs %q", r.LastChange.Summary, c.Summary)
	}
}
