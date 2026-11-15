package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/token"
)

// ADR 0035, #1234: a door verb's kernel part (calendar_kern, person_*_kern,
// aufgabe_kern) runs only on the door's Auftrag, which the kernel signs for
// one caller and one verb for two minutes. laib-haushalt hands it back and
// is the caller for that part only; without it, with another verb's, an
// expired or a forged one, or from a hand not behind the door, it runs
// nothing. It is no tool in anyone's list.
func TestAKernelPartRunsOnlyOnTheDoorsAuftragAsTheCaller(t *testing.T) {
	env, _ := testBox(t)
	store, _ := token.Load(cli.TokenPath(env))
	hh, _, _ := store.Mint("haushalt", 0, "test")
	cl, _, _ := store.Mint("claude", 0, "test")
	srv := httptest.NewServer(mcp.Handler(env))
	t.Cleanup(srv.Close)
	call := func(tok, auftrag, tool, args string) (string, bool) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": map[string]string{"args": args}}})
		req, _ := http.NewRequest("POST", srv.URL, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("X-Laib-Ceiling", "propose")
		req.Header.Set("X-Laib-Auftrag", auftrag)
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
		return r.Result.Content[0].Text, r.Result.IsError
	}
	soon := time.Now().Add(time.Minute)
	good := mcp.Auftrag(env, "human:anna", "calendar", soon)
	if out, isErr := call(hh, good, "laib_calendar_kern", "list"); isErr || !strings.Contains(out, `"kalender": false`) {
		t.Fatalf("on its Auftrag: %s", out)
	}
	// calendar_kern is tier apply: the haushalt hand (propose) reaches it only as the caller
	for name, c := range map[string][2]string{
		"no Auftrag":             {hh, ""},
		"another verb's":         {hh, mcp.Auftrag(env, "human:anna", "aufgabe", soon)},
		"expired":                {hh, mcp.Auftrag(env, "human:anna", "calendar", time.Now().Add(-time.Second))},
		"forged":                 {hh, strings.Replace(good, good[:4], "AAAA", 1)},
		"not behind the door":    {cl, good},
		"a verb that is no part": {hh, good},
	} {
		tool := "laib_calendar_kern"
		if name == "a verb that is no part" {
			tool = "laib_status"
		}
		if out, isErr := call(c[0], c[1], tool, "list"); !isErr {
			t.Errorf("%s: ran: %s", name, out)
		}
	}
	for _, tool := range mcp.Tools(mustState(t, env), "human") {
		if strings.HasSuffix(tool["name"].(string), "_kern") {
			t.Errorf("%s is in the tool list", tool["name"])
		}
	}
	if code := runArgs([]string{"calendar_kern", "list"}); code == cli.ExitOK {
		t.Error("calendar_kern ran from the shell")
	}
}
