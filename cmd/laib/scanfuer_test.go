package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
)

// Owner 2026-10-28 (mdopp/haus#191): a scanned stack belongs to the
// household or to one person. Haus sends fuer: "person" with person on
// start; the household's sends no field. laib writes fuer into the order as
// given, for dokumente to read, and refuses any other value, fuer on another
// action, and "person" without a person.
func TestAScanOrderSaysWhomTheStackIsFor(t *testing.T) {
	env, dataDir := testBox(t)
	withHaushalt(t, env)
	svc := filepath.Join(env.Paths.State, "services", "scan")
	os.MkdirAll(svc, 0o755)
	os.WriteFile(filepath.Join(svc, "laib.yml"), []byte("name: scan\nprovides: [scan]\nhealth:\n  none: a loop\nstatus:\n  file: ${DATA_DIR}/scan/laib-status.json\n"), 0o644)
	os.WriteFile(filepath.Join(svc, "scan.container"), []byte("[Container]\nImage=x\n"), 0o644)
	if err := proposal.CommitDirty(context.Background(), env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	call := func(args map[string]any) (string, int) {
		st, err := cli.LoadState(env)
		if err != nil {
			t.Fatal(err)
		}
		return mcp.Call(context.Background(), env, st, "haus", "laib_scan", args)
	}
	dir := filepath.Join(dataDir, "scan", "auftrag")
	order := func(args map[string]any) map[string]string {
		t.Helper()
		os.RemoveAll(dir)
		if out, code := call(args); code != cli.ExitOK {
			t.Fatalf("%v: exit %d: %s", args, code, out)
		}
		orders, _ := filepath.Glob(filepath.Join(dir, "*.json"))
		if len(orders) != 1 {
			t.Fatalf("%v: want one order, got %v", args, orders)
		}
		b, _ := os.ReadFile(orders[0])
		var m map[string]string
		if json.Unmarshal(b, &m) != nil {
			t.Fatalf("%s", b)
		}
		return m
	}
	if m := order(map[string]any{"action": "start", "person": "mama", "fuer": "person"}); m["fuer"] != "person" || m["person"] != "mama" {
		t.Fatalf("only for mama: %v", m)
	}
	if m := order(map[string]any{"action": "start", "fuer": "haushalt"}); m["fuer"] != "haushalt" {
		t.Fatalf("the household's, said: %v", m)
	}
	if m := order(map[string]any{"action": "start", "person": "mama"}); m["fuer"] != "" {
		t.Fatalf("the household's, by default: %v", m)
	}

	os.RemoveAll(dir)
	out, code := call(map[string]any{"action": "start", "fuer": "person"})
	if code == cli.ExitOK || !strings.Contains(out, "für wen") {
		t.Fatalf("fuer person without a person: exit %d: %s", code, out)
	}
	for _, bad := range []map[string]any{
		{"action": "start", "person": "mama", "fuer": "mich"},
		{"action": "weiter", "fuer": "haushalt"},
		{"action": "redo", "stapel": "2026-10-22T10-15-00", "person": "mama", "fuer": "person"},
		{"action": "start", "fuer": "person", "person": "mama", "fuer2": "x"},
	} {
		if out, code := call(bad); code == cli.ExitOK {
			t.Errorf("%v was taken: %s", bad, out)
		}
	}
	if orders, _ := filepath.Glob(filepath.Join(dir, "*.json")); len(orders) != 0 {
		t.Fatalf("a refused call left an order: %v", orders)
	}
}
