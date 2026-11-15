package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/envelope"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/token"
)

// #1238: write takes up to envelope.MaxWrite and refuses one byte more,
// in German, without touching the file.
func TestWriteTakesUpToItsLimitAndRefusesAbove(t *testing.T) {
	env, data, _ := envBox(t)
	p := filepath.Join(data, "whoami", "cfg", "big.bin")
	if code := writeAs(env, "whoami", p, strings.Repeat("a", envelope.MaxWrite)); code != cli.ExitOK {
		t.Fatalf("a write of exactly MaxWrite: exit %d", code)
	}
	var errOut bytes.Buffer
	e := &cliEnv{Actor: env.Actor, Paths: env.Paths, In: strings.NewReader(strings.Repeat("b", envelope.MaxWrite+1)), ErrOut: &errOut, Out: &errOut}
	if code := envelope.Write(context.Background(), e, []string{"whoami", p}); code == cli.ExitOK {
		t.Fatal("a write above MaxWrite was accepted")
	}
	if !strings.Contains(errOut.String(), "zu groß") {
		t.Fatalf("refusal not said: %q", errOut.String())
	}
	if b, _ := os.ReadFile(p); len(b) != envelope.MaxWrite || b[0] != 'a' {
		t.Fatalf("the refused write changed the file (%d bytes)", len(b))
	}
}

// #1238: read above MaxRead says truncated and the full size instead of
// returning cut JSON as if it were whole; below it, truncated is false.
func TestReadSaysWhenItTruncates(t *testing.T) {
	env, data, _ := envBox(t)
	read := func(n int) map[string]any {
		t.Helper()
		p := filepath.Join(data, "whoami", "cfg", "r.txt")
		os.WriteFile(p, []byte(strings.Repeat("x", n)), 0o644)
		var out bytes.Buffer
		e := &cliEnv{Actor: env.Actor, Paths: env.Paths, JSON: true, Out: &out, ErrOut: &out}
		if code := envelope.Read(context.Background(), e, []string{"whoami", p}); code != cli.ExitOK {
			t.Fatalf("read: exit %d: %s", code, out.String())
		}
		var r map[string]any
		if err := json.Unmarshal(out.Bytes(), &r); err != nil {
			t.Fatalf("read's answer is not whole JSON: %v", err)
		}
		return r
	}
	if r := read(100); r["truncated"] != false || r["size"] != float64(100) {
		t.Fatalf("small file: %v %v", r["truncated"], r["size"])
	}
	r := read(envelope.MaxRead + 10)
	if r["truncated"] != true || r["size"] != float64(envelope.MaxRead+10) || len(r["content"].(string)) != envelope.MaxRead {
		t.Fatalf("big file: truncated=%v size=%v len=%d", r["truncated"], r["size"], len(r["content"].(string)))
	}
}

// #1238: the MCP body fits a MaxWrite content JSON-escaped, and a body
// above the limit gets an answer, not a reset connection.
func TestMCPBodyFitsAWriteAndAnswersAbove(t *testing.T) {
	esc, _ := json.Marshal(string(bytes.Repeat([]byte{1}, envelope.MaxWrite)))
	if len(esc) > mcp.MaxBody {
		t.Fatalf("a MaxWrite content escaped is %d bytes, over MaxBody %d", len(esc), mcp.MaxBody)
	}
	env, _ := testBox(t)
	store, _ := token.Load(cli.TokenPath(env))
	tok, _, _ := store.Mint("claude", 0, "test")
	srv := httptest.NewServer(mcp.Handler(env))
	t.Cleanup(srv.Close)
	req, _ := http.NewRequest("POST", srv.URL, bytes.NewReader(make([]byte, mcp.MaxBody+1)))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("an oversized body was not answered: %v", err)
	}
	defer resp.Body.Close()
	var r struct{ Error struct{ Message string } }
	json.NewDecoder(resp.Body).Decode(&r)
	if !strings.Contains(r.Error.Message, "zu groß") {
		t.Fatalf("answer: %+v", r)
	}
}
