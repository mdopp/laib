package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/token"
)

// ADR 0026, 0027 (#598, #624): a verb served by laib-haushalt. The kernel
// checks hand, tier and verb before anything goes out, --help is the
// kernel's; then it hands the call over HTTP to the program's container,
// with the door's key, the caller's actor, the verb's ceiling and what the
// program may know of the box (the LAN address, the scan service's status
// and orders folder). Its output and exit code are the verb's; what it
// journals the kernel writes, as the caller.
func TestTheVerbDoorHandsTheCallOverOnlyAfterPolicy(t *testing.T) {
	env, _ := testBox(t)
	key := doorKey(t, env)
	type got struct {
		path, auth string
		body       map[string]any
	}
	var calls []got
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var c map[string]any
		json.NewDecoder(r.Body).Decode(&c)
		calls = append(calls, got{r.URL.Path, r.Header.Get("Authorization"), c})
		json.NewEncoder(w).Encode(map[string]any{"out": "ja\n", "err": "nein\n", "code": 3,
			"journal": []any{map[string]string{"verb": "approve", "actor": "human:mdopp", "service": "scan", "result": "info", "detail": "start"}}})
	}))
	t.Cleanup(stub.Close)
	t.Setenv("LAIB_HAUSHALT_URL", stub.URL)
	st, err := cli.LoadState(env)
	if err != nil {
		t.Fatal(err)
	}
	if out, code := mcp.Call(context.Background(), env, st, "resident", "laib_scan", map[string]any{"action": "start"}); code != cli.ExitRefused || len(calls) != 0 {
		t.Fatalf("a read hand reached the program: exit %d, %d calls: %s", code, len(calls), out)
	}

	since := time.Now().Add(-time.Minute)
	var out, errOut strings.Builder
	cenv := &cli.Env{Paths: env.Paths, Actor: "haus", JSON: true, Out: &out, ErrOut: &errOut}
	if code := verb(t, "scan").Run(context.Background(), cenv, []string{"start", "--person", "mama"}); code != 3 {
		t.Fatalf("exit %d, want the program's 3", code)
	}
	if len(calls) != 1 || calls[0].path != "/call" || calls[0].auth != "Bearer "+key {
		t.Fatalf("the call did not go to /call with the door's key: %+v", calls)
	}
	b := calls[0].body
	argv, _ := json.Marshal(b["argv"])
	if string(argv) != `["scan","start","--person","mama"]` || b["json"] != true || b["actor"] != "haus" || b["ceiling"] != "propose" || b["lanIp"] != "10.0.0.2" || b["scan"] != nil {
		t.Fatalf("the program got %v", b)
	}
	if out.String() != "ja\n" || errOut.String() != "nein\n" {
		t.Fatalf("stdout %q, stderr %q", out.String(), errOut.String())
	}
	es, _ := journal.Read(env.Paths.Journal, since)
	var mine []journal.Entry
	for _, e := range es {
		if e.Service == "scan" {
			mine = append(mine, e)
		}
	}
	if len(mine) != 1 || mine[0].Actor != "haus" || mine[0].Verb != "scan" || mine[0].Detail != "start" {
		t.Fatalf("the program's journal line, as the caller: %+v", mine)
	}
	out.Reset()
	if code := verb(t, "scan").Run(context.Background(), cenv, []string{"--help"}); code != cli.ExitOK || !strings.Contains(out.String(), "laib scan [propose]") || len(calls) != 1 {
		t.Fatalf("--help: exit %d, %d calls: %s", code, len(calls), out.String())
	}
	// net geraete is a read: its ceiling is read
	verb(t, "net").Run(context.Background(), cenv, []string{"geraete"})
	if len(calls) != 2 || calls[1].body["ceiling"] != "read" {
		t.Fatalf("net geraete went out with %v", calls[len(calls)-1].body)
	}

	// a door nobody answers says who and where to look, at once
	stub.Close()
	errOut.Reset()
	if code := verb(t, "scan").Run(context.Background(), cenv, []string{"status"}); code == cli.ExitOK || !strings.Contains(errOut.String(), "laib-haushalt") {
		t.Fatalf("an unreachable laib-haushalt: exit %d: %s", code, errOut.String())
	}
}

// ADR 0027: laib-haushalt's own token is the haushalt hand's, which may
// only propose. Its proposals wait for a decision even where the dial
// would approve the class itself (values: auto), and a call back lowers
// its ceiling further with X-Laib-Ceiling (net geraete is a read). The
// control: the same proposal by bote is the dial's.
func TestTheHaushaltHandProposesAndTheDialDecidesNothingOfIt(t *testing.T) {
	env, _ := testBox(t)
	store, _ := token.Load(cli.TokenPath(env))
	hh, _, _ := store.Mint("haushalt", 0, "test")
	bote, _, _ := store.Mint("bote", 0, "test")
	srv := httptest.NewServer(mcp.Handler(env))
	t.Cleanup(srv.Close)
	call := func(tok, ceiling, tool, args string) (string, bool) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": map[string]string{"args": args}}})
		req, _ := http.NewRequest("POST", srv.URL, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		if ceiling != "" {
			req.Header.Set("X-Laib-Ceiling", ceiling)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var r struct {
			Result struct {
				Content []struct{ Text string } `json:"content"`
				IsError bool                    `json:"isError"`
			} `json:"result"`
		}
		json.NewDecoder(resp.Body).Decode(&r)
		if len(r.Result.Content) == 0 {
			return "", r.Result.IsError
		}
		return r.Result.Content[0].Text, r.Result.IsError
	}
	propose := func(tok, ceiling string) (*proposal.Proposal, string) {
		t.Helper()
		out, isErr := call(tok, ceiling, "laib_propose", `--set services/whoami/values.yml:GREETING=hallo "Gruß setzen"`)
		var p proposal.Proposal
		if isErr || json.Unmarshal([]byte(out), &p) != nil || p.ID == 0 {
			return nil, out
		}
		got, _ := proposal.Get(cli.InboxDir(env), p.ID)
		return got, out
	}
	p, out := propose(hh, "")
	if p == nil || p.Status != "open" || p.Actor != "haushalt" {
		t.Fatalf("haushalt's proposal is not open, waiting for a decision: %+v %s", p, out)
	}
	if out, isErr := call(hh, "", "laib_approve", "1"); !isErr {
		t.Fatalf("haushalt approved: %s", out)
	}
	if p, out := propose(hh, "read"); p != nil {
		t.Fatalf("a call back under ceiling read proposed: %s", out)
	}
	propose(bote, "")
	all, _ := proposal.List(cli.InboxDir(env), false)
	if i := slices.IndexFunc(all, func(o *proposal.Proposal) bool { return o.Actor == "bote" }); i < 0 || all[i].Status == "open" {
		t.Fatalf("the control: bote's values proposal is the dial's (values: auto): %+v", all)
	}
	if ps, _ := proposal.List(cli.InboxDir(env), true); !slices.ContainsFunc(ps, func(o *proposal.Proposal) bool { return o.Actor == "haushalt" }) {
		t.Fatalf("haushalt's proposal left the inbox: %+v", ps)
	}
}
