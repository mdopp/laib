package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
)

// #596: the Bote, told by the owner who an unknown number is, files the number
// as a box.yml people proposal (laib_person_signal); only a human lets it in,
// and the Bote cannot. The reason that reaches the chat carries no number.
func TestTheBoteProposesASignalNumberAndOnlyAHumanLetsItIn(t *testing.T) {
	env, _ := testBox(t)
	box, _ := os.ReadFile(filepath.Join(env.Paths.State, "box.yml"))
	out, code := mcp.Call(context.Background(), env, mustState(t, env), "bote", "laib_person_signal", map[string]any{"user": "mama", "number": "+4900000002"})
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	var got struct {
		Proposal int    `json:"proposal"`
		Message  string `json:"message"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.Proposal == 0 || !strings.Contains(got.Message, "Freigabe") {
		t.Fatalf("%v: %s", err, out)
	}
	if now, _ := os.ReadFile(filepath.Join(env.Paths.State, "box.yml")); string(now) != string(box) {
		t.Fatal("box.yml changed on main: a proposal waits for the owner")
	}
	p, err := proposal.Get(cli.InboxDir(env), got.Proposal)
	if err != nil {
		t.Fatal(err)
	}
	if p.Class != "human" || !slices.Equal(p.Files, []string{"box.yml"}) || !strings.Contains(strings.Join(p.Reasons, " "), "people") || strings.Contains(p.Reason, "+49") {
		t.Fatalf("a human's proposal, box.yml only, saying it changes people, no number in the reason: %+v", p)
	}
	if _, code := mcp.Call(context.Background(), env, mustState(t, env), "bote", "laib_approve", map[string]any{"args": "1"}); code == cli.ExitOK {
		t.Fatal("the Bote approved its own proposal")
	}
	human := *env
	if code := inbox.Approve(context.Background(), &human, []string{"1"}); code != cli.ExitOK {
		t.Fatalf("approve: exit %d", code)
	}
	if ch := mustState(t, env).Box.People["mama"]; !slices.Equal(ch, []string{"signal:+4900000002"}) {
		t.Fatalf("people.mama after the approval: %v", ch)
	}
	// changing it later is the same verb; Haus calls it from the person page
	if out, code := mcp.Call(context.Background(), env, mustState(t, env), "haus", "laib_person_signal", map[string]any{"user": "mama", "number": "+4900000009"}); code != cli.ExitOK {
		t.Fatalf("haus: exit %d: %s", code, out)
	}
	// not a number: refused, no proposal
	for _, n := range []string{"0151 000", "4900000002", "+49 000 0002"} {
		if out, code := mcp.Call(context.Background(), env, mustState(t, env), "bote", "laib_person_signal", map[string]any{"user": "mama", "number": n}); code == cli.ExitOK {
			t.Errorf("%q accepted: %s", n, out)
		}
	}
	if all, _ := proposal.List(cli.InboxDir(env), false); len(all) != 2 {
		t.Fatalf("%d proposals, want the approved one and Haus's", len(all))
	}
	if _, code := mcp.Call(context.Background(), env, mustState(t, env), "resident", "laib_person_signal", map[string]any{"user": "mama", "number": "+4900000002"}); code == cli.ExitOK {
		t.Fatal("the resident hand proposed a number")
	}
}

// #596: the invite takes the number; the account is made as before and the
// number waits as a proposal, which the answer names. A bad number is refused
// before the account is made.
func TestInviteWithASignalNumberFilesTheProposal(t *testing.T) {
	env, calls := personBox(t, `{"user":"anna","name":"Anna Muster","email":"anna@box.test","mailed":true,"expires":"2026-10-19T10:00:00Z"}`)
	args := map[string]any{"user": "anna", "first": "Anna", "email": "anna@box.test", "signal": "0151"}
	if out, code := mcp.Call(context.Background(), env, mustState(t, env), "haus", "laib_person_invite", args); code == cli.ExitOK || len(*calls) != 0 {
		t.Fatalf("a bad number: exit %d, %d tool runs: %s", code, len(*calls), out)
	}
	args["signal"] = "+4900000005"
	out, code := mcp.Call(context.Background(), env, mustState(t, env), "haus", "laib_person_invite", args)
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	var inv Invite
	if err := json.Unmarshal([]byte(out), &inv); err != nil || inv.Proposal == 0 || !strings.Contains(inv.Message, "#") || !inv.Mailed {
		t.Fatalf("%v: %s", err, out)
	}
	p, err := proposal.Get(cli.InboxDir(env), inv.Proposal)
	if err != nil || p.Class != "human" {
		t.Fatalf("%v: %+v", err, p)
	}
	b, _ := os.ReadFile(filepath.Join(env.Paths.State, "box.yml"))
	if strings.Contains(string(b), "+4900000005") {
		t.Fatal("the number is in box.yml before anyone approved it")
	}
}
