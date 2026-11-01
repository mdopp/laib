package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
)

// #978: approved Operator wishes never ended — no status, nothing closed
// them when they turned out to be noise or done. A wish now carries its
// status in its file in the state repo (offen, erledigt, verworfen, with
// the reason, when and who); laib wish list and status read and set it,
// one commit and one journal line each; the Inbox shows it. No ticket.
func TestAWishHasAStatusInTheStateRepo(t *testing.T) {
	env, _ := testBox(t)
	ctx := context.Background()
	st, err := cli.LoadState(env)
	if err != nil {
		t.Fatal(err)
	}
	// the log review files one through MCP, in its skill's name
	out, code := mcp.Call(mcp.InSkill(ctx, "durchsicht"), env, st, "ops", "laib_wish", map[string]any{"text": "whoami antwortet nicht auf /health", "saw": "whoami.service 5×, geprüft: nicht während eines geplanten Neustarts", "better": "wartung"})
	if code != cli.ExitOK {
		t.Fatalf("wish: exit %d: %s", code, out)
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	env.Actor = "human:mdopp"
	if code := inbox.Approve(ctx, env, []string{strconv.Itoa(open[0].ID)}); code != cli.ExitOK {
		t.Fatalf("approve: exit %d", code)
	}
	list := func(args ...string) []inbox.Wish {
		t.Helper()
		out, code := mcp.Call(ctx, env, st, "ops", "laib_wish", map[string]any{"args": strings.Join(append([]string{"list"}, args...), " ")})
		var ws []inbox.Wish
		if err := json.Unmarshal([]byte(out), &ws); code != cli.ExitOK || err != nil {
			t.Fatalf("wish list: exit %d: %v: %s", code, err, out)
		}
		return ws
	}
	ws := list()
	if len(ws) != 1 || ws[0].Status != "offen" || ws[0].Skill != "durchsicht" || !ws[0].Operator || strings.Join(ws[0].Services, ",") != "whoami" || ws[0].Since.IsZero() {
		t.Fatalf("one open wish of durchsicht about whoami: %+v", ws)
	}

	// the Bote and Haus close nothing
	if out, code := mcp.Call(ctx, env, st, "bote", "laib_wish", map[string]any{"args": "status", "stdin": ws[0].ID + " erledigt weg"}); code == cli.ExitOK {
		t.Fatalf("the bote closed a wish: %s", out)
	}
	// the review closes it, with its reason; a line it cannot read is said, the others still run
	out, code = mcp.Call(ctx, env, st, "ops", "laib_wish", map[string]any{"args": "status", "stdin": ws[0].ID + " verworfen würde heute nicht gemeldet: während eines geplanten Neustarts\n2026-01-01T00-00-00.000000 erledigt x"})
	if code == cli.ExitOK || !strings.Contains(out, "verworfen würde heute nicht gemeldet") {
		t.Fatalf("status: exit %d: %s", code, out)
	}
	b, _ := os.ReadFile(filepath.Join(env.Paths.State, inbox.WishFile(ws[0].ID)))
	if !strings.HasPrefix(string(b), "# Wunsch: whoami antwortet nicht auf /health\n") || !strings.Contains(string(b), "\n\n---\nstatus: verworfen\ngrund: würde heute nicht gemeldet: während eines geplanten Neustarts\nam: ") {
		t.Fatalf("the wish file:\n%s", b)
	}
	if log, _ := exec.Command("git", "-C", env.Paths.State, "log", "-1", "--format=%an %s").Output(); !strings.HasPrefix(string(log), "ops wish verworfen "+ws[0].ID) {
		t.Fatalf("one commit on main by ops: %q", log)
	}
	if es, _ := journal.Newest(env.Paths.Journal, ws[0].Since.AddDate(0, 0, -1), 1, func(e journal.Entry) bool { return e.Verb == "wish" }); len(es) != 1 || es[0].Detail != "verworfen: würde heute nicht gemeldet: während eines geplanten Neustarts" {
		t.Fatalf("the journal line the morning report reads: %+v", es)
	}
	if len(list()) != 0 || len(list("--alle")) != 1 || list("--alle")[0].Grund == "" {
		t.Fatalf("list shows the open ones, --alle all: %+v", list("--alle"))
	}
	// the Inbox's „Zuletzt entschieden“ says what came of it
	page := inboxMux(env)("GET", "/inbox", nil)
	if !strings.Contains(page, "Wunsch verworfen: würde heute nicht gemeldet") {
		t.Fatalf("the Inbox does not show the wish's status:\n%s", page)
	}
	// a human opens it again
	env.Actor = "human:mdopp"
	if code := cmdWish(ctx, env, []string{"status", ws[0].ID, "offen"}); code != cli.ExitOK || list()[0].Status != "offen" {
		t.Fatalf("offen again: exit %d", code)
	}
}
