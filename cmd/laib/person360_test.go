package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
)

// personBox: testBox with an identity service whose person.py is the
// given fake (apply.RunHook swapped: the hooks image is not here).
func personBox(t *testing.T, reply string) (*cliEnv, *[][]string) {
	t.Helper()
	env, _ := testBox(t)
	svc := filepath.Join(env.Paths.State, "services", "auth")
	os.MkdirAll(svc, 0o755)
	os.WriteFile(filepath.Join(svc, "laib.yml"), []byte("name: auth\nprovides: [identity]\nsecrets:\n  - name: LLDAP_ADMIN_PASSWORD\n"), 0o644)
	os.WriteFile(filepath.Join(svc, "values.yml"), []byte("IMAGE: docker.io/lldap/lldap:stable\n"), 0o644)
	os.WriteFile(filepath.Join(svc, "auth.container"), []byte("[Container]\nImage=${IMAGE}\n\n[Install]\nWantedBy=default.target\n"), 0o644)
	os.WriteFile(filepath.Join(svc, "person.py"), []byte("# the tool\n"), 0o644)
	box := filepath.Join(env.Paths.State, "box.yml")
	b, _ := os.ReadFile(box)
	os.WriteFile(box, bytes.Replace(b, []byte("roles: {}"), []byte("roles: {identity: auth}"), 1), 0o644)
	if err := proposal.CommitDirty(context.Background(), env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.LoadState(env); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	real := apply.RunHook
	t.Cleanup(func() { apply.RunHook = real })
	apply.RunHook = func(_ context.Context, h apply.HookSpec) (string, error) {
		calls = append(calls, h.Command)
		env, _ := os.ReadFile(h.EnvFile)
		if !strings.Contains(string(env), "LAIB_SECRET_LLDAP_ADMIN_PASSWORD=") {
			t.Errorf("the tool does not get the identity service's secrets:\n%s", env)
		}
		return "LLDAP: anna created\n" + reply + "\n", nil
	}
	return env, &calls
}

// #360: the haus hand invites a resident through the MCP tool Haus calls
// (laib_person_invite): typed arguments, resend a JSON boolean, the German
// sentence and the link back, the journal line.
func TestHausInvitesAResidentAndGetsTheLinkWithoutSMTP(t *testing.T) {
	env, calls := personBox(t, `{"user":"anna","name":"Anna Muster","email":"anna@box.test","mailed":false,"link":"https://auth.box.test/reset-password/step2?token=t","expires":"2026-10-19T10:00:00Z"}`)
	st, err := cli.LoadState(env)
	if err != nil {
		t.Fatal(err)
	}
	out, code := mcp.Call(context.Background(), env, st, "haus", "laib_person_invite",
		map[string]any{"user": "anna", "first": "Anna", "last": "Muster", "email": "anna@box.test", "group": "family", "resend": false})
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	var inv Invite
	if err := json.Unmarshal([]byte(out), &inv); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if inv.Mailed || inv.Link == "" || inv.Expires != "2026-10-19T10:00:00Z" || !strings.Contains(inv.Message, "verschickt keine Mails") || !strings.Contains(inv.Message, "19.10. 12:00") {
		t.Fatalf("invite: %+v", inv)
	}
	want := []string{"python3", filepath.Join(env.Paths.State, "services", "auth", "person.py"), "invite", "anna", "Anna", "Muster", "anna@box.test", "family", "0"}
	if len(*calls) != 1 || !slices.Equal((*calls)[0], want) {
		t.Fatalf("tool argv %q, want %q", *calls, want)
	}
	// a resend from Haus: no group, resend true; the group the user has stays (the tool's "-")
	if out, code := mcp.Call(context.Background(), env, st, "haus", "laib_person_invite",
		map[string]any{"user": "anna", "first": "Anna", "last": "Muster", "email": "anna@box.test", "resend": true}); code != cli.ExitOK {
		t.Fatalf("resend: exit %d: %s", code, out)
	}
	if got := (*calls)[1][7:]; !slices.Equal(got, []string{"-", "1"}) {
		t.Fatalf("resend argv tail %q", got)
	}
	if _, code := mcp.Call(context.Background(), env, st, "haus", "laib_person_invite", map[string]any{"user": "anna", "first": "A", "email": "a@b.c", "resend": "yes"}); code == cli.ExitOK {
		t.Fatal("a string for resend was taken")
	}
	all, _ := journal.Read(env.Paths.Journal, time.Time{})
	var lines []string
	for _, e := range all {
		if e.Verb == "person" {
			lines = append(lines, e.Actor+" "+e.Detail)
		}
	}
	if !slices.Equal(lines, []string{"haus invite anna (mailed false)", "haus resend anna (mailed false)"}) {
		t.Fatalf("journal: %q", lines)
	}
}

// Admins are a human's call: the haus hand gets a sentence, the tool never runs.
func TestOnlyAHumanInvitesAnAdmin(t *testing.T) {
	env, calls := personBox(t, `{"user":"bob","name":"Bob","email":"bob@box.test","mailed":true,"expires":"2026-10-19T10:00:00Z"}`)
	st, _ := cli.LoadState(env)
	out, code := mcp.Call(context.Background(), env, st, "haus", "laib_person_invite", map[string]any{"user": "bob", "first": "Bob", "email": "bob@box.test", "group": "admins"})
	if code == cli.ExitOK || len(*calls) != 0 || !strings.Contains(out, "nur ein Mensch") || strings.Contains(out, "may not") {
		t.Fatalf("haus invited an admin: exit %d %s", code, out)
	}
	var buf bytes.Buffer
	env.Out = &buf
	argv := personArgs([]string{"person", "invite", "bob", "--first", "Bob", "--email", "bob@box.test", "--group", "admins"})
	if argv[0] != "person_invite" {
		t.Fatalf("laib person invite is %q", argv)
	}
	if code := cmdPersonInvite(context.Background(), env, argv[1:]); code != cli.ExitOK {
		t.Fatalf("the human: exit %d", code)
	}
	if !strings.Contains(buf.String(), "Bob ist eingeladen") || len(*calls) != 1 || (*calls)[0][7] != "admins" {
		t.Fatalf("human invite: %s %q", buf.String(), *calls)
	}
}

// A refusal is the tool's own sentence; pending lists who waits.
func TestPersonRefusalAndPending(t *testing.T) {
	env, _ := personBox(t, `{"error":"Den Benutzer anna gibt es schon. Eine neue Mail schickt „erneut senden“."}`)
	st, _ := cli.LoadState(env)
	out, code := mcp.Call(context.Background(), env, st, "haus", "laib_person_invite", map[string]any{"user": "anna", "first": "Anna", "email": "anna@box.test"})
	if code == cli.ExitOK || !strings.Contains(out, "gibt es schon") {
		t.Fatalf("exit %d: %s", code, out)
	}
	env, calls := personBox(t, `[{"user":"ben","name":"Ben","email":"ben@box.test","invited_at":"2026-10-18T10:00:00Z"}]`)
	st, _ = cli.LoadState(env)
	out, code = mcp.Call(context.Background(), env, st, "haus", "laib_person_pending", map[string]any{})
	var rows []Pending
	if code != cli.ExitOK || json.Unmarshal([]byte(out), &rows) != nil || len(rows) != 1 || rows[0].User != "ben" || (*calls)[0][2] != "pending" {
		t.Fatalf("pending: exit %d %s %q", code, out, *calls)
	}
	if _, code := mcp.Call(context.Background(), env, st, "resident", "laib_person_invite", map[string]any{"user": "x", "first": "X", "email": "x@b.c"}); code == cli.ExitOK {
		t.Fatal("a read-only hand invited someone")
	}
}
