package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
	"github.com/mdopp/laib/internal/token"
)

// #500: disable revoked the ops token but left OPERATOR_TOKEN in
// secrets.age; enable minted only when that secret was missing, so after
// disable → enable the operator ran with a revoked bearer (401 on every
// call) while status said "läuft".
func TestOperatorEnableAfterDisableRunsWithAValidToken(t *testing.T) {
	c := newCbox(t)
	if err := state.SetBlock(filepath.Join(c.env.Paths.State, "box.yml"), "model", "url", "http://127.0.0.1:1/v1"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mcp.Handler(c.env))
	defer srv.Close()
	// what the operator does: the bearer from its podman secret (ADR 0027), against MCP
	call := func() int {
		t.Helper()
		b, _ := sysd.Run(context.Background(), "podman", "secret", "inspect", "--showsecret", "--format", "x", "laib.laib-operator.laib-operator.LAIB_TOKEN")
		m := regexp.MustCompile(`"([^"]+)"`).FindStringSubmatch(b)
		if m == nil {
			t.Fatalf("no podman secret with the operator's token: %q", b)
		}
		req, _ := http.NewRequest(http.MethodPost, srv.URL, bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)))
		req.Header.Set("Authorization", "Bearer "+string(m[1]))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	run := func(args ...string) {
		t.Helper()
		if rc := cmdOperator(context.Background(), c.env, args); rc != cli.ExitOK {
			t.Fatalf("operator %v rc %d", args, rc)
		}
	}
	unauthorized := func() bool {
		t.Helper()
		st, err := cli.LoadState(c.env)
		if err != nil {
			t.Fatal(err)
		}
		return diag.OperatorFacts(context.Background(), c.env, st).Unauthorized
	}
	run("enable")
	if code := call(); code != http.StatusOK {
		t.Fatalf("first enable: MCP answers %d", code)
	}
	run("disable")
	run("enable")
	if code := call(); code != http.StatusOK {
		t.Fatalf("enable after disable: MCP answers %d, the operator runs with the revoked token", code)
	}
	if unauthorized() {
		t.Fatal("a freshly minted token reads as refused")
	}
	// revoked behind its back (laib token revoke): status and diag say so
	store, _ := token.Load(cli.TokenPath(c.env))
	for _, r := range store.List() {
		if r.Hand == "ops" {
			store.Revoke(r.ID)
		}
	}
	if !unauthorized() {
		t.Fatal("a revoked operator token does not show")
	}
	word, f := diag.OperatorState(diag.OpFacts{Enabled: true, UnitActive: true, Answers: true, Unauthorized: true, LastNote: time.Now(), Now: time.Now(), Tick: 10 * time.Minute})
	if word != "abgewiesen" || f == nil || f.Severity != "rot" {
		t.Fatalf("revoked token: %q %+v", word, f)
	}
	// and enable heals it without a disable first
	run("enable")
	if code := call(); code != http.StatusOK || unauthorized() {
		t.Fatalf("enable over a revoked token: MCP answers %d", code)
	}
}
