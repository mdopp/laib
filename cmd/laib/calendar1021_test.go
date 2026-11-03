package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/secrets"
)

// laib#1021, ADR 0031: the household's calendar account was haus and is
// homes. A subscription stored before the rename (the box's Müllabfuhr) is
// homes's: list shows it there, every and unsubscribe find it under homes,
// and the next write stores it as homes, so kalender mirrors it into homes/.
func TestASubscriptionStoredForHausIsHomess(t *testing.T) {
	env, _ := testBox(t)
	b, _ := json.Marshal([]Abo{{Person: "haus", Name: "muellabfuhr", Kind: "abo", Title: "Müllabfuhr", URL: "https://example.org/abfuhr.ics", Since: "2026-10-21T00:00:00Z"}})
	sec, err := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	if err != nil {
		t.Fatal(err)
	}
	sec[abosSecret] = base64.RawURLEncoding.EncodeToString(b)
	if err := secrets.Save(env.Paths.Secrets, env.Paths.Key, sec); err != nil {
		t.Fatal(err)
	}
	call := func(args map[string]any) (string, int) {
		st, err := cli.LoadState(env)
		if err != nil {
			t.Fatal(err)
		}
		return mcp.Call(context.Background(), env, st, "homes", "laib_calendar", args)
	}
	if out, _ := call(map[string]any{"action": "list"}); !strings.Contains(out, `"person": "homes"`) || strings.Contains(out, `"haus"`) {
		t.Fatalf("list: %s", out)
	}
	if out, code := call(map[string]any{"action": "every", "person": "homes", "name": "muellabfuhr", "every": "30d"}); code != cli.ExitOK {
		t.Fatalf("every under homes: exit %d: %s", code, out)
	}
	sec, _ = secrets.Load(env.Paths.Secrets, env.Paths.Key)
	raw, _ := base64.RawURLEncoding.DecodeString(sec[abosSecret])
	if !strings.Contains(string(raw), `"person":"homes","name":"muellabfuhr"`) || !strings.Contains(string(raw), "abfuhr.ics") {
		t.Fatalf("not stored as homes's, or the address lost: %q", raw)
	}
}
