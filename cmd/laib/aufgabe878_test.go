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
	"github.com/mdopp/laib/internal/mcp"
)

func ids(list []aufgabeOutTask) []int {
	var out []int
	for _, a := range list {
		out = append(out, a.ID)
	}
	return out
}

// aufgabeOutTask is the task as Haus decodes it (docs/INTERFACE.md, #878).
type aufgabeOutTask struct {
	ID         int    `json:"id"`
	Fuer       string `json:"fuer"`
	FertigWenn string `json:"fertig_wenn"`
	Weg        *struct {
		Seite     string `json:"seite"`
		Gespraech bool   `json:"gespraech"`
	} `json:"weg"`
}

func callTasks(t *testing.T, env *cliEnv, hand string, args map[string]any) (offen int, list []aufgabeOutTask, out string, code int) {
	t.Helper()
	out, code = mcp.Call(context.Background(), env, mustState(t, env), hand, "laib_aufgabe", args)
	var got struct {
		Offen    int              `json:"offen"`
		Aufgaben []aufgabeOutTask `json:"aufgaben"`
		Aufgabe  aufgabeOutTask   `json:"aufgabe"`
	}
	if code == cli.ExitOK {
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	if got.Aufgaben == nil && got.Aufgabe.ID != 0 {
		got.Aufgaben = []aufgabeOutTask{got.Aufgabe}
	}
	return got.Offen, got.Aufgaben, out, code
}

// #878 (haus#188): a task is for the admins (as before), for everyone, or
// for one person; it names its guided way, a Haus page or a conversation
// with the Bote. Haus and the Bote act for one person (von): the kernel
// shows them that person's tasks and never another's, whatever Haus asks.
func TestATaskIsForSomeoneAndHausSeesOnlyItsPersonsOwn(t *testing.T) {
	env, _ := testBox(t)
	t.Setenv("LAIB_DISK", "/nonexistent")
	st := mustState(t, env)
	tools := mcp.Tools(st, "haus")
	i := slices.IndexFunc(tools, func(m map[string]any) bool { return m["name"] == "laib_aufgabe" })
	props := tools[i]["inputSchema"].(map[string]any)["properties"].(map[string]any)
	for _, p := range []string{"fuer", "weg", "fertig_wenn", "befund", "von"} {
		if props[p] == nil {
			t.Errorf("laib_aufgabe lacks %s", p)
		}
	}
	if e := props["action"].(map[string]any)["enum"].([]string); !slices.Contains(e, "zeigen") {
		t.Errorf("action enum %v lacks zeigen", e)
	}

	add := func(hand string, args map[string]any) aufgabeOutTask {
		t.Helper()
		args["action"] = "add"
		_, l, out, code := callTasks(t, env, hand, args)
		if code != cli.ExitOK || len(l) != 1 {
			t.Fatalf("add %v: exit %d: %s", args, code, out)
		}
		return l[0]
	}
	if a := add("claude", map[string]any{"titel": "Sicherung außer Haus", "befund": "backup offsite"}); a.Fuer != "admins" || a.FertigWenn != "backup offsite" || a.Weg != nil {
		t.Fatalf("an old-shape add is the admins' with its befund as fertig_wenn: %+v", a)
	}
	if a := add("claude", map[string]any{"titel": "Stimme aufnehmen", "fuer": "person:anna", "weg": "/stimme"}); a.Fuer != "person:anna" || a.Weg == nil || a.Weg.Seite != "/stimme" {
		t.Fatalf("anna's: %+v", a)
	}
	if a := add("haus", map[string]any{"titel": "Joshua auf Signal", "fuer": "person:bert", "weg": "gespraech", "von": "mama"}); a.Fuer != "person:bert" || a.Weg == nil || !a.Weg.Gespraech {
		t.Fatalf("bert's: %+v", a)
	}
	if a := add("ops", map[string]any{"titel": "Haus-App koppeln", "fuer": "alle", "fertig_wenn": "watchdog"}); a.Fuer != "alle" || a.FertigWenn != "watchdog" {
		t.Fatalf("everyone's: %+v", a)
	}
	for _, bad := range []map[string]any{
		{"titel": "x", "fuer": "anna"}, {"titel": "x", "fuer": "person:Anna"}, {"titel": "x", "weg": "https://evil.example/"},
		{"titel": "x", "fertig_wenn": "Watch Dog!"}, {"titel": "x", "befund": "raid", "fertig_wenn": "watchdog"},
	} {
		bad["action"] = "add"
		if _, _, out, code := callTasks(t, env, "claude", bad); code == cli.ExitOK {
			t.Errorf("%v was accepted: %s", bad, out)
		}
	}

	for _, c := range []struct {
		hand, von string
		want      []int
	}{
		{"claude", "", []int{1, 2, 3, 4}},
		{"haus", "", []int{1, 4}},
		{"haus", "anna", []int{1, 2, 4}},
		{"haus", "bert", []int{1, 3, 4}},
		{"bote", "anna", []int{1, 2, 4}},
		{"bote", "", []int{1, 4}},
	} {
		args := map[string]any{"action": "list"}
		if c.von != "" {
			args["von"] = c.von
		}
		offen, l, out, code := callTasks(t, env, c.hand, args)
		if code != cli.ExitOK || !slices.Equal(ids(l), c.want) || offen != len(c.want) {
			t.Errorf("%s for %q sees %v (offen %d), want %v: %s", c.hand, c.von, ids(l), offen, c.want, out)
		}
	}

	// zeigen: one task, by the same rule; another person's is not there
	if _, l, out, code := callTasks(t, env, "bote", map[string]any{"action": "zeigen", "id": "2", "von": "anna"}); code != cli.ExitOK || len(l) != 1 || l[0].ID != 2 {
		t.Fatalf("the Bote reads anna's task for anna: exit %d: %s", code, out)
	}
	for _, c := range []struct{ hand, von, id string }{{"bote", "bert", "2"}, {"haus", "", "2"}, {"haus", "anna", "3"}} {
		_, _, out, code := callTasks(t, env, c.hand, map[string]any{"action": "zeigen", "id": c.id, "von": c.von})
		if code == cli.ExitOK || !strings.Contains(out, "gibt es nicht") || strings.Contains(out, "Joshua") || strings.Contains(out, "Stimme") {
			t.Errorf("%s for %q read #%s: exit %d: %s", c.hand, c.von, c.id, code, out)
		}
	}
	// ticking off another person's task: as if it were not there
	if _, _, out, code := callTasks(t, env, "haus", map[string]any{"action": "erledigt", "id": "2", "von": "bert"}); code == cli.ExitOK || !strings.Contains(out, "gibt es nicht") {
		t.Fatalf("Haus for bert ticked anna's task off: %s", out)
	}
	if _, _, out, code := callTasks(t, env, "haus", map[string]any{"action": "erledigt", "id": "2", "von": "anna"}); code != cli.ExitOK {
		t.Fatalf("Haus for anna ticks her own off: %s", out)
	}
	if _, _, out, code := callTasks(t, env, "bote", map[string]any{"action": "erledigt", "id": "3", "von": "bert"}); code == cli.ExitOK {
		t.Fatalf("the Bote ticked a task off: %s", out)
	}

	// readiness counts the tasks that are nobody's own: the admins' and everyone's
	out, code := mcp.Call(context.Background(), env, mustState(t, env), "haus", "laib_status", map[string]any{"fresh": true})
	var rd struct {
		Ready struct {
			Aufgaben int `json:"aufgaben"`
		} `json:"ready"`
	}
	if code != cli.ExitOK || json.Unmarshal([]byte(out), &rd) != nil || rd.Ready.Aufgaben != 2 {
		t.Fatalf("readiness: two open tasks that are nobody's own: %d %s", code, out)
	}

	b, _ := os.ReadFile(filepath.Join(env.Paths.State, "aufgaben.yml"))
	for _, want := range []string{"fuer: person:anna", "seite: /stimme", "gespraech: true", "fertig_wenn: backup offsite"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("aufgaben.yml lacks %q:\n%s", want, b)
		}
	}
	if strings.Contains(string(b), "befund:") {
		t.Errorf("aufgaben.yml writes the old key:\n%s", b)
	}
}

// A fertig_wenn on a probe whose silence is no measurement: the task is
// kept, and the answer says a person ticks it off, not Laib.
func TestAFertigWennLaibCannotMeasureSaysSo(t *testing.T) {
	env, _ := testBox(t)
	t.Setenv("LAIB_DISK", "/nonexistent")
	out, code := mcp.Call(context.Background(), env, mustState(t, env), "claude", "laib_aufgabe", map[string]any{"action": "add", "titel": "/boot aufräumen", "fertig_wenn": "boot"})
	if code != cli.ExitOK || !strings.Contains(out, "hakt sie nicht selbst ab") {
		t.Fatalf("exit %d: %s", code, out)
	}
	out, code = mcp.Call(context.Background(), env, mustState(t, env), "claude", "laib_aufgabe", map[string]any{"action": "add", "titel": "Wächter", "fertig_wenn": "watchdog"})
	if code != cli.ExitOK || !strings.Contains(out, "hakt sie selbst ab") || strings.Contains(out, "nicht selbst") {
		t.Fatalf("exit %d: %s", code, out)
	}
}
