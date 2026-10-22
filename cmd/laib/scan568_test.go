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

// #568: Haus's "Stapel scannen", the Bote's "scann den Stapel" and the
// owner's CLI reach the scan service through laib_scan: status answers the
// service's own status file, every other action is one order file in its
// data folder that the service picks up. laib starts nothing itself and
// decides nothing; a resident's read token may not order a scan.
func TestHausAndTheBoteOrderAScanThroughLaib(t *testing.T) {
	env, dataDir := testBox(t)
	call := func(hand string, args map[string]any) (string, int) {
		st, err := cli.LoadState(env)
		if err != nil {
			t.Fatal(err)
		}
		return mcp.Call(context.Background(), env, st, hand, "laib_scan", args)
	}
	if out, code := call("haus", map[string]any{"action": "status"}); code != cli.ExitRefused || !strings.Contains(out, "nicht eingerichtet") {
		t.Fatalf("a box without the scan service: exit %d: %s", code, out)
	}

	svc := filepath.Join(env.Paths.State, "services", "scan")
	os.MkdirAll(svc, 0o755)
	os.WriteFile(filepath.Join(svc, "laib.yml"), []byte("name: scan\nprovides: [scan]\nhealth:\n  none: a loop\nstatus:\n  file: ${DATA_DIR}/scan/laib-status.json\n"), 0o644)
	os.WriteFile(filepath.Join(svc, "scan.container"), []byte("[Container]\nImage=x\n"), 0o644)
	if err := proposal.CommitDirty(context.Background(), env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	if out, code := call("haus", map[string]any{"action": "status"}); code != cli.ExitRefused || !strings.Contains(out, "noch keinen Zustand") {
		t.Fatalf("no status file yet: exit %d: %s", code, out)
	}
	os.MkdirAll(filepath.Join(dataDir, "scan"), 0o755)
	os.WriteFile(filepath.Join(dataDir, "scan", "laib-status.json"), []byte(`{"at":"2026-10-22T10:00:00Z","detail":"bereit","zustand":"bereit","scanner":true}`), 0o644)
	out, code := call("haus", map[string]any{"action": "status"})
	var status map[string]any
	if code != cli.ExitOK || json.Unmarshal([]byte(out), &status) != nil || status["zustand"] != "bereit" || status["scanner"] != true {
		t.Fatalf("status: exit %d: %s", code, out)
	}

	if out, code := call("haus", map[string]any{"action": "start", "person": "mama"}); code != cli.ExitOK || !strings.Contains(out, "angenommen") {
		t.Fatalf("start: exit %d: %s", code, out)
	}
	if out, code := call("bote", map[string]any{"action": "redo", "stapel": "2026-10-22T10-15-00"}); code != cli.ExitOK {
		t.Fatalf("the Bote's redo: exit %d: %s", code, out)
	}
	orders, _ := filepath.Glob(filepath.Join(dataDir, "scan", "auftrag", "*.json"))
	if len(orders) != 2 {
		t.Fatalf("want two orders, got %v", orders)
	}
	var got []map[string]string
	for _, o := range orders {
		var m map[string]string
		b, _ := os.ReadFile(o)
		if json.Unmarshal(b, &m) != nil {
			t.Fatalf("%s: %s", o, b)
		}
		got = append(got, m)
	}
	if got[0]["aktion"] != "start" || got[0]["person"] != "mama" || !strings.HasPrefix(got[0]["von"], "haus") {
		t.Fatalf("first order: %v", got[0])
	}
	if got[1]["aktion"] != "redo" || got[1]["stapel"] != "2026-10-22T10-15-00" {
		t.Fatalf("second order: %v", got[1])
	}

	for _, bad := range []map[string]any{{"action": "redo"}, {"action": "redo", "stapel": "../../etc"}, {"action": "start", "person": "Mama Dopp"}} {
		if out, code := call("haus", bad); code == cli.ExitOK {
			t.Errorf("%v was taken: %s", bad, out)
		}
	}
	if out, code := call("resident", map[string]any{"action": "start"}); code == cli.ExitOK {
		t.Fatalf("a resident's read token ordered a scan: %s", out)
	}
	if orders, _ := filepath.Glob(filepath.Join(dataDir, "scan", "auftrag", "*.json")); len(orders) != 2 {
		t.Fatalf("a refused call left an order: %v", orders)
	}
}
