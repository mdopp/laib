package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/secrets"
)

// #1194: proposals chain. B waits for A: approved first, B applies only
// after A, in that order, without a second tap; a card waiting for a key
// says so with the key's form and applies once the key is set; a newer
// card for the same value closes the older; a rejected link leaves the
// cards after it open, saying why.
// Ablauf L-11
func TestApprovalsChainAndApplyInOrder(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	t.Setenv("LAIB_DISK", "/nonexistent")
	svc := filepath.Join(env.Paths.State, "services", "whoami")
	b, _ := os.ReadFile(filepath.Join(svc, "laib.yml"))
	os.WriteFile(filepath.Join(svc, "laib.yml"), append(b, []byte("  - name: WHO_MAIL\n    external: true\n    optional: true\n    owner: {label: \"Whoami: Post\", warum: \"Damit whoami Post liest.\", hilfe: \"Beim Anbieter.\"}\n")...), 0o644)
	if err := proposal.CommitDirty(ctx, env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	other, human := &cliEnv{Paths: env.Paths, Actor: "human:other"}, &cliEnv{Paths: env.Paths, Actor: "human:test"}
	propose := func(set string, more ...string) int {
		t.Helper()
		if code := inbox.Propose(ctx, other, append(append([]string{"--set", "services/whoami/values.yml:" + set}, more...), "setzt "+set)); code != cli.ExitOK {
			t.Fatalf("propose %s: %d", set, code)
		}
		open, _ := proposal.List(cli.InboxDir(env), true)
		return open[0].ID
	}
	get := func(id int) *proposal.Proposal { p, _ := proposal.Get(cli.InboxDir(env), id); return p }
	values := func() string { b, _ := os.ReadFile(filepath.Join(svc, "values.yml")); return string(b) }

	if code := inbox.Propose(ctx, other, []string{"--set", "services/whoami/values.yml:X=1", "--wartet", "vorschlag:99x", "x"}); code == cli.ExitOK {
		t.Error("a wait laib cannot measure is refused")
	}
	if code := inbox.Propose(ctx, other, []string{"--set", "services/whoami/values.yml:X=1", "--wartet", "schluessel:WHOAMI_TOKEN", "x"}); code == cli.ExitOK {
		t.Error("a key the box makes is nothing the owner brings")
	}

	a := propose("FIRST=a")
	bID := propose("SECOND=b", "--wartet", "vorschlag:"+strconv.Itoa(a))
	if code := inbox.Approve(ctx, human, []string{strconv.Itoa(bID)}); code != cli.ExitOK {
		t.Fatalf("approve B: %d", code)
	}
	if p := get(bID); p.Status != "open" || p.Freigabe != "human:test" || strings.Contains(values(), "SECOND") {
		t.Fatalf("B approved first applies nothing: %+v\n%s", p, values())
	}
	if ws := inbox.WaitsOf(env, get(bID)); len(ws) != 1 || ws[0].Text != "wartet auf Vorschlag #"+strconv.Itoa(a) || ws[0].Link != "https://laib.box.test/inbox#"+strconv.Itoa(a) {
		t.Errorf("B's card says what it waits for, with the link: %+v", ws)
	}
	if code := inbox.Approve(ctx, human, []string{strconv.Itoa(a)}); code != cli.ExitOK {
		t.Fatalf("approve A: %d", code)
	}
	pa, pb := get(a), get(bID)
	if pb.Status != "approved" || !strings.Contains(values(), "SECOND: b") || !strings.Contains(values(), "FIRST: a") || !pa.Decided.Before(pb.Decided) || pa.Batch == pb.Batch {
		t.Fatalf("A then B, each its own run: A %+v\nB %+v", pa, pb)
	}
	es, _ := journal.Read(env.Paths.Journal, time.Time{})
	if i := slices.IndexFunc(es, func(e journal.Entry) bool {
		return e.Verb == "approve" && strings.HasPrefix(e.Detail, "#"+strconv.Itoa(bID)+" von human:other freigegeben, wartet")
	}); i < 0 {
		t.Errorf("the journal says B was approved in advance: %+v", es)
	}

	// a card that waits for a key
	k := propose("MAIL=!secret WHO_MAIL", "--wartet", "schluessel:WHO_MAIL")
	inbox.Approve(ctx, human, []string{strconv.Itoa(k)})
	if ws := inbox.WaitsOf(env, get(k)); get(k).Status != "open" || len(ws) != 1 || ws[0].Text != "wartet auf den Schlüssel „Whoami: Post“" || ws[0].Link != "https://laib.box.test/schluessel?name=WHO_MAIL" {
		t.Fatalf("waits for the key, with its form: %+v %+v", get(k), ws)
	}
	sec, _ := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	sec["WHO_MAIL"] = "geheim"
	secrets.Save(env.Paths.Secrets, env.Paths.Key, sec)
	inbox.Carry(ctx, &cliEnv{Paths: env.Paths, Actor: "laib"}) // as after any run, the ten-minute converge's too
	if p := get(k); p.Status != "approved" || p.DecidedBy != "human:test" {
		t.Fatalf("the key is set: applied without a second tap, as the one who approved: %+v", p)
	}

	// a newer card for the same value closes the older one
	old := propose("IMAGE=docker.io/traefik/whoami:v1.11")
	dep := propose("AFTER=x", "--wartet", "vorschlag:"+strconv.Itoa(old))
	newer := propose("IMAGE=docker.io/traefik/whoami:v1.12")
	if p := get(old); p.Status != "stale" || p.Refiled != newer || !strings.Contains(p.StaleReason, "abgelöst durch #"+strconv.Itoa(newer)) {
		t.Fatalf("the older image card is superseded: %+v", p)
	}
	if ws := inbox.WaitsOf(env, get(dep)); len(ws) != 1 || ws[0].Text != "wartet auf Vorschlag #"+strconv.Itoa(newer) {
		t.Errorf("what waited for the old card waits for its successor: %+v", ws)
	}
	// a rejected link: the card after it stays open and says why
	inbox.Reject(ctx, human, []string{strconv.Itoa(newer), "nein"})
	inbox.Approve(ctx, human, []string{strconv.Itoa(dep)})
	if ws := inbox.WaitsOf(env, get(dep)); get(dep).Status != "open" || len(ws) != 1 || !ws[0].Nie || !strings.Contains(ws[0].Text, "abgelehnt ist: diesen ablehnen oder neu vorschlagen") {
		t.Fatalf("the card after a rejected one stays open, saying why: %+v %+v", get(dep), ws)
	}
}
