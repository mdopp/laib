package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/registry"
)

// routineBox is testBox with whoami pinned by digest a, and laib updates'
// newest digest for it b, seen at seen.
func routineBox(t *testing.T, image string, seen time.Time) *cliEnv {
	t.Helper()
	env, _ := testBox(t)
	os.WriteFile(filepath.Join(env.Paths.State, "services", "whoami", "values.yml"), []byte("IMAGE: "+image+"@"+digest("a")+"\nTOKEN: !secret WHOAMI_TOKEN\n"), 0o644)
	if err := proposal.CommitDirty(context.Background(), env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	old := inbox.Seen
	t.Cleanup(func() { inbox.Seen = old })
	inbox.Seen = func(_, service, key string) (string, time.Time) {
		if service == "whoami" && key == "IMAGE" {
			return digest("b"), seen
		}
		return "", time.Time{}
	}
	return env
}

func proposeRoutine(t *testing.T, env *cliEnv, hand, image, word string) *proposal.Proposal {
	t.Helper()
	st, _ := cli.LoadState(env)
	out, code := mcp.Call(context.Background(), env, st, hand, "laib_propose", map[string]any{"reason": "Routine: whoami", "sets": []any{"whoami/IMAGE=" + image + "@" + digest("b")},
		"notes": []any{"whoami/IMAGE=Behebt einen Fehler."}, "routine": word})
	if code != cli.ExitOK {
		t.Fatalf("propose: exit %d: %s", code, out)
	}
	all, _ := proposal.List(cli.InboxDir(env), false)
	if len(all) != 1 {
		t.Fatalf("want one proposal: %+v", all)
	}
	return all[0]
}

// ADR 0037: a normal new image of a running service applies itself —
// approved by laib, applied, marked as a routine for the morning report and
// `laib updates` — once a third party's waited its soak; inside the soak, or
// from another hand, it stays a card that says why. Ablauf L-70, L-72
func TestARoutineImageAppliesItself(t *testing.T) {
	const img = "docker.io/traefik/whoami:v1.10"
	env := routineBox(t, img, time.Now().Add(-4*24*time.Hour))
	p := proposeRoutine(t, env, "ops", img, "normal")
	if p.Status != "approved" || p.DecidedBy != "laib" || p.Routine != "normal" || p.Notes["whoami/IMAGE"] != "Behebt einen Fehler." {
		t.Fatalf("a soaked routine image is not approved by laib: %+v", p)
	}
	es, _ := journal.Read(env.Paths.Journal, time.Time{})
	if !strings.Contains(journalText(es), "als Routine (normal, ADR 0037)") {
		t.Fatalf("the journal does not say it applied itself: %s", journalText(es))
	}
	for _, c := range []struct {
		name, hand, word string
		seen             time.Time
		want             string
	}{
		{"inside the soak", "ops", "normal", time.Now().Add(-24 * time.Hour), "whoami wartet noch bis"},
		{"another hand", "claude", "normal", time.Now().Add(-4 * 24 * time.Hour), "nur der Operator"},
	} {
		env := routineBox(t, img, c.seen)
		p := proposeRoutine(t, env, c.hand, img, c.word)
		if p.Status != "open" || p.Class != "human" || p.Routine != "" || !strings.Contains(strings.Join(p.Reasons, " "), c.want) {
			t.Fatalf("%s: want an open card saying %q: %+v", c.name, c.want, p)
		}
	}
	env = routineBox(t, img, time.Now())
	if p := proposeRoutine(t, env, "ops", img, "sicherheit"); p.Status != "approved" || p.Routine != "sicherheit" {
		t.Fatalf("a security fix skips the soak: %+v", p)
	}
}

// ADR 0037: our own image (signed by its CI) skips the soak. Ablauf L-72
func TestOurOwnRoutineImageSkipsTheSoak(t *testing.T) {
	const img = "ghcr.io/mdopp/homes:latest"
	env := routineBox(t, img, time.Now())
	cosignRefuses(t, nil)
	if p := proposeRoutine(t, env, "ops", img, "normal"); p.Status != "approved" || p.Routine != "normal" {
		t.Fatalf("our own image waited: %+v", p)
	}
}

// ADR 0037: rights never apply themselves — a new port beside the image is a
// card even with --routine; and box.yml updates.auto: aus turns it off, as
// `laib updates --auto aus` proposes. Ablauf L-73, L-75
func TestRightsAndTheSwitchKeepTheCard(t *testing.T) {
	ctx := context.Background()
	const img = "docker.io/traefik/whoami:v1.10"
	env := routineBox(t, img, time.Now().Add(-4*24*time.Hour))
	st, _ := cli.LoadState(env)
	patch := "diff --git a/services/whoami/whoami.container b/services/whoami/whoami.container\n--- a/services/whoami/whoami.container\n+++ b/services/whoami/whoami.container\n@@ -1,4 +1,5 @@\n [Container]\n Image=${IMAGE}\n+PublishPort=0.0.0.0:8080:80\n Environment=TOKEN=${TOKEN}\n AutoUpdate=registry\n"
	if out, code := mcp.Call(ctx, env, st, "ops", "laib_propose", map[string]any{"reason": "Routine: whoami", "patch": patch, "routine": "normal"}); code != cli.ExitOK {
		t.Fatalf("propose: exit %d: %s", code, out)
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	if len(open) != 1 || open[0].Routine != "" || !strings.Contains(strings.Join(open[0].Reasons, " "), "nicht von selbst eingespielt") {
		t.Fatalf("a port applied itself: %+v", open)
	}
	env = routineBox(t, img, time.Now().Add(-4*24*time.Hour))
	env.Actor = "human:mdopp"
	if code := cmdUpdates(ctx, env, []string{"--auto", "aus"}); code != cli.ExitOK {
		t.Fatalf("--auto aus: exit %d", code)
	}
	open, _ = proposal.List(cli.InboxDir(env), true)
	if len(open) != 1 || open[0].Class != "human" || !strings.Contains(proposal.Diff(ctx, env.Paths.State, open[0]), "auto: aus") {
		t.Fatalf("--auto aus is a box.yml card for a person: %+v", open)
	}
	if code := inbox.Approve(ctx, env, []string{"1"}); code != cli.ExitOK {
		t.Fatalf("approve: exit %d", code)
	}
	p := proposeRoutine2(t, env, img)
	if p.Status != "open" || !strings.Contains(strings.Join(p.Reasons, " "), "updates.auto: aus") {
		t.Fatalf("switched off, a routine applied itself: %+v", p)
	}
	if code := cmdUpdates(ctx, env, []string{"--auto", "vielleicht"}); code == cli.ExitOK {
		t.Fatal("--auto takes an or aus only")
	}
}

// proposeRoutine2 files the routine beside what is already in the inbox and returns it.
func proposeRoutine2(t *testing.T, env *cliEnv, image string) *proposal.Proposal {
	t.Helper()
	st, _ := cli.LoadState(env)
	if out, code := mcp.Call(context.Background(), env, st, "ops", "laib_propose", map[string]any{"reason": "Routine: whoami", "sets": []any{"whoami/IMAGE=" + image + "@" + digest("b")}, "routine": "normal"}); code != cli.ExitOK {
		t.Fatalf("propose: exit %d: %s", code, out)
	}
	all, _ := proposal.List(cli.InboxDir(env), false)
	return all[0]
}

func journalText(es []journal.Entry) string {
	var b strings.Builder
	for _, e := range es {
		b.WriteString(e.Verb + " " + e.Detail + "\n")
	}
	return b.String()
}

// ADR 0037: `laib updates` says per new digest whether it applies itself or
// waits (a third party three days from when laib first saw it, our own at
// once), its JSON carries ready for versionen, and it lists what applied
// itself in seven days and what is held for a person. Ablauf L-76
func TestUpdatesSaysWhatAppliesWaitsAndApplied(t *testing.T) {
	ctx := context.Background()
	const img = "docker.io/traefik/whoami:v1.10"
	env := routineBox(t, img, time.Now().Add(-4*24*time.Hour))
	proposeRoutine(t, env, "ops", img, "normal")
	realAsk := registry.Ask
	t.Cleanup(func() { registry.Ask = realAsk })
	registry.Ask = func(context.Context, *cli.Env, registry.Call) (string, error) {
		b, _ := json.Marshal([]registry.ImageUpdate{{Service: "whoami", Key: "IMAGE", Image: "docker.io/traefik/whoami", Current: "v1.10", Digest: digest("c")},
			{Service: "homes", Key: "IMAGE", Image: "ghcr.io/mdopp/homes", Current: "latest", Digest: digest("d")}})
		return string(b), nil
	}
	var out bytes.Buffer
	env.Out = &out
	if code := cmdUpdates(ctx, env, nil); code != cli.ExitOK {
		t.Fatalf("updates: exit %d", code)
	}
	s := out.String()
	for _, want := range []string{"(wartet bis ", "(spielt sich ein, wenn das Modell es normal findet)", "eingespielt    #1 Routine: whoami"} {
		if !strings.Contains(s, want) {
			t.Errorf("laib updates does not say %q:\n%s", want, s)
		}
	}
	out.Reset()
	env.JSON = true
	cmdUpdates(ctx, env, nil)
	var rows []registry.ImageUpdate
	json.Unmarshal(out.Bytes(), &rows)
	ready, _ := time.Parse(time.RFC3339, rows[0].Ready)
	if d := time.Until(ready); d < 71*time.Hour || d > 73*time.Hour || rows[1].Ready == "" || time.Until(mustTime(rows[1].Ready)) > time.Minute {
		t.Fatalf("ready: a third party in three days, our own now: %+v", rows)
	}
}

func mustTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// ADR 0037: what applied itself last night keeps its card in its place for
// a day, with „Zurücknehmen“ — where the morning report's line points —
// while another decision of the same hour is under „Zuletzt entschieden“. Ablauf L-74
func TestARoutineCardStaysADayWithItsWayBack(t *testing.T) {
	env, _ := testBox(t)
	at := time.Now().UTC().Add(-5 * time.Hour).Format(time.RFC3339)
	os.MkdirAll(cli.InboxDir(env), 0o755)
	for id, routine := range map[int]string{7: `"routine":"normal",`, 8: ""} {
		body := fmt.Sprintf(`{"id":%d,"actor":"ops",%s"branch":"proposal/%d","tier":"apply","status":"approved","created":%q,"decided":%q,"decidedBy":"laib","reason":"Routine: whoami","files":["services/whoami/values.yml"],"class":"human","kind":"values","changes":[{"op":"~","service":"whoami","key":"IMAGE","old":"a@sha256:1","new":"a@sha256:2"}],"outcome":"applied"}`, id, routine, id, at, at)
		os.WriteFile(fmt.Sprintf("%s/%d.json", cli.InboxDir(env), id), []byte(body), 0o644)
	}
	page := inboxMux(env)("GET", "/inbox", nil)
	if c := cardOf(page, 7); !strings.Contains(c, "<section") || !strings.Contains(c, "/inbox/7/rollback") {
		t.Fatalf("the routine card is not in its place with its way back:\n%s", page)
	}
	if c := cardOf(page, 8); strings.Contains(c, "<section") {
		t.Fatalf("another decision of five hours ago is in its place:\n%s", c)
	}
}
