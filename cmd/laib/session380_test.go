package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/token"
)

// #380: the Werkstatt holds a token of the narrow hand `werkstatt`, which can
// do one thing: get a `claude` token for one session, with claude's TTL from
// policy.yml, journaled, listed with the session's name. No long-lived
// apply token at rest; an expired one ends that session's access only.
func TestASessionGetsItsOwnShortClaudeToken(t *testing.T) {
	env, _ := testBox(t)
	store, err := token.Load(cli.TokenPath(env))
	if err != nil {
		t.Fatal(err)
	}
	werkstatt, _, err := store.Mint("werkstatt", 0, "LAIB_WERKSTATT_TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mcp.Handler(env))
	defer srv.Close()
	call := func(bearer, tool, args string) (int, string, bool) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": map[string]any{"args": args}}})
		req, _ := http.NewRequest(http.MethodPost, srv.URL, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+bearer)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var rpc struct {
			Result struct {
				Content []struct{ Text string }
				IsError bool
			}
		}
		json.NewDecoder(res.Body).Decode(&rpc)
		text := ""
		if len(rpc.Result.Content) > 0 {
			text = rpc.Result.Content[0].Text
		}
		return res.StatusCode, text, rpc.Result.IsError
	}
	session := func(name string) (string, time.Time) {
		t.Helper()
		code, out, isErr := call(werkstatt, "laib_session", name)
		if code != 200 || isErr {
			t.Fatalf("laib_session %s: %d %s", name, code, out)
		}
		var got struct {
			Token, Hand string
			Expires     time.Time
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil || got.Token == "" || got.Hand != "claude" {
			t.Fatalf("laib_session answered %q", out)
		}
		return got.Token, got.Expires
	}

	laib, exp := session("laib")
	if d := time.Until(exp); d <= 0 || d > 8*time.Hour {
		t.Fatalf("the session token expires in %s, want at most claude's ttl 8h", d)
	}
	if code, out, isErr := call(laib, "laib_status", ""); code != 200 || isErr {
		t.Fatalf("the session's token does not reach /mcp as claude: %d %s", code, out)
	}
	haus, _ := session("haus")
	if haus == laib {
		t.Fatal("two sessions share a token")
	}
	store, _ = token.Load(cli.TokenPath(env))
	var notes []string
	for _, r := range store.List() {
		if r.Hand == "claude" {
			notes = append(notes, r.Note)
		}
	}
	if strings.Join(notes, ",") != "session werkstatt: laib,session werkstatt: haus" {
		t.Fatalf("token list shows %q, want one per session with its name", notes)
	}
	es, _ := journal.Read(env.Paths.Journal, time.Time{})
	minted := 0
	for _, e := range es {
		if e.Verb == "session" && strings.Contains(e.Detail, "for claude") {
			minted++
		}
	}
	if minted != 2 {
		t.Fatalf("%d session mints journaled, want 2: %+v", minted, es)
	}

	// laib's token runs out: refused; haus's still works; the next session works without the owner
	for i := range store.Records {
		if store.Records[i].Note == "session werkstatt: laib" {
			store.Records[i].Expires = time.Now().Add(-time.Minute)
		}
	}
	b, _ := json.Marshal(store.Records)
	os.WriteFile(cli.TokenPath(env), b, 0o600)
	if code, _, _ := call(laib, "laib_status", ""); code != http.StatusUnauthorized {
		t.Fatalf("an expired session token answered %d", code)
	}
	if code, out, isErr := call(haus, "laib_status", ""); code != 200 || isErr {
		t.Fatalf("one session's expiry ended another's: %d %s", code, out)
	}
	again, _ := session("laib")
	if code, out, isErr := call(again, "laib_status", ""); code != 200 || isErr {
		t.Fatalf("the next session does not get in: %d %s", code, out)
	}
	store, _ = token.Load(cli.TokenPath(env))
	for _, r := range store.Records {
		if r.Expired(time.Now()) {
			t.Fatalf("an expired session token is still in tokens.json: %+v", r)
		}
	}

	// the werkstatt hand can do nothing else; claude cannot make sessions
	if _, out, isErr := call(werkstatt, "laib_status", ""); !isErr {
		t.Fatalf("the werkstatt hand reached laib_status: %s", out)
	}
	if _, out, isErr := call(again, "laib_session", "x"); !isErr {
		t.Fatalf("a claude session minted itself another token: %s", out)
	}
}

func TestPolicyRefusesAnOpenEndedOrChainedSession(t *testing.T) {
	base := func(target state.Hand) state.Policy {
		return state.Policy{Tiers: policy.Tiers, Hands: map[string]state.Hand{
			"werkstatt": {Ceiling: "read", Verbs: []string{"session"}, Session: "claude"},
			"claude":    target, "human": {Ceiling: "destroy"},
		}}
	}
	if err := policy.Validate(base(state.Hand{Ceiling: "apply", TTL: "8h"})); err != nil {
		t.Fatalf("the werkstatt hand as laib init writes it: %v", err)
	}
	for name, h := range map[string]state.Hand{
		"no ttl":  {Ceiling: "apply"},
		"bad ttl": {Ceiling: "apply", TTL: "lang"},
		"chained": {Ceiling: "apply", TTL: "8h", Session: "claude"},
		"unknown": {},
	} {
		p := base(h)
		if name == "unknown" {
			delete(p.Hands, "claude")
		}
		if err := policy.Validate(p); err == nil {
			t.Errorf("%s: a session hand passed validation", name)
		}
	}
	p := base(state.Hand{Ceiling: "apply", TTL: "8h"})
	p.Hands["werkstatt"] = state.Hand{Ceiling: "read", Session: "human"}
	if policy.Validate(p) == nil {
		t.Error("a session of the human hand passed validation")
	}
}
