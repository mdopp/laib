package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/render"
)

func cmdApproveFor(t *testing.T, env *cliEnv, id string) int {
	t.Helper()
	for _, v := range mcp.Verbs {
		if v.Name == "approve" {
			return v.Run(context.Background(), env, []string{id})
		}
	}
	t.Fatal("no approve verb")
	return 0
}

// #441: laib-notify tells the owner through the Bote first. box.yml
// notify.person names them in people; the unit carries bote-notify's
// address, the person and the Inbox's link, the env file the Bote's
// notify token; the notify hand also reads the inbox (the proposals it
// announces). The Inbox is behind Authelia's second factor.
func TestTheNotifierTellsTheOwnerThroughTheBote(t *testing.T) {
	b := newRbox(t)
	box := filepath.Join(b.env.Paths.State, "box.yml")
	raw, _ := os.ReadFile(box)
	withPerson := strings.Replace(string(raw), "notify:\n", "notify:\n  person: martin\n", 1)
	os.WriteFile(box, []byte(withPerson), 0o644)
	if _, err := cli.LoadState(b.env); err == nil || !strings.Contains(err.Error(), "notify.person") {
		t.Fatalf("notify.person who is not in people must fail the load: %v", err)
	}
	os.WriteFile(box, []byte(withPerson+"people:\n  martin: [\"discord:123456\"]\n"), 0o644)
	st, err := cli.LoadState(b.env)
	if err != nil {
		t.Fatal(err)
	}
	sec := map[string]string{render.NotifyToken: "tok-n", "BOTE_NOTIFY_TOKEN": "bote-n"}
	out, err := render.Render(st, sec)
	if err != nil {
		t.Fatal(err)
	}
	unit := out["quadlet/laib-notify/laib-notify.container"]
	for _, w := range []string{`Environment="NOTIFY_PERSON=martin"`, `Environment="BOTE_NOTIFY_URL=http://127.0.0.1:8645"`, `Environment="LAIB_INBOX_URL=https://laib.`} {
		if !strings.Contains(unit, w) {
			t.Errorf("the notifier's unit lacks %s (a person is enough to run it):\n%s", w, unit)
		}
	}
	if strings.Contains(unit, "bote-n") || render.PodmanSecrets(st, sec)["laib.laib-notify.laib-notify.BOTE_NOTIFY_TOKEN"] != "bote-n" {
		t.Errorf("the Bote's token belongs in a podman secret, not the unit:\n%s", unit)
	}
	var tools []string
	for _, tl := range mcp.Tools(st, "notify") {
		tools = append(tools, tl["name"].(string))
	}
	slices.Sort(tools)
	if strings.Join(tools, " ") != "laib_diag laib_inbox laib_note laib_updates" {
		t.Errorf("the notify hand reaches %v, want diag, inbox, note, updates", tools)
	}
	access := out["authelia/access.yml"]
	if access == "" {
		for k, v := range out {
			if strings.Contains(k, "authelia") && strings.Contains(v, "access_control") {
				access = v
			}
		}
	}
	if !strings.Contains(access, "laib."+st.Box.Domain+"\n      policy: two_factor") {
		t.Errorf("the Inbox is not behind the second factor:\n%s", access)
	}
}
