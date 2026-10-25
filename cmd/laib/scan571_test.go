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

// #571: Haus sets the scanner up. einrichten is a proposal, never a change:
// without the scan service it adds the template with the chosen device,
// with it it sets SCANNER; main stays as it was. suchen, pruefen and probe
// are orders the service carries out. A device name that could break the
// unit (a quote, a newline, systemd's % and $) is refused.
func TestHausSetsUpTheScannerByProposalOnly(t *testing.T) {
	env, dataDir := testBox(t)
	withHaushalt(t, env) // served behind the verb door since #598
	call := func(args map[string]any) (string, int) {
		st, err := cli.LoadState(env)
		if err != nil {
			t.Fatal(err)
		}
		return mcp.Call(context.Background(), env, st, "haus", "laib_scan", args)
	}
	proposed := func(out string, code int) int {
		t.Helper()
		var got struct {
			Proposal int    `json:"proposal"`
			Message  string `json:"message"`
		}
		if code != cli.ExitOK || json.Unmarshal([]byte(out), &got) != nil || got.Proposal == 0 || !strings.Contains(got.Message, "Vorschlag") {
			t.Fatalf("exit %d: %s", code, out)
		}
		return got.Proposal
	}
	values := filepath.Join(env.Paths.State, "services", "scan", "values.yml")
	for _, name := range []string{"paperless", "file-share"} { // what scan's after names
		dir := filepath.Join(env.Paths.State, "services", name)
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "laib.yml"), []byte("name: "+name+"\nhealth:\n  none: a stub\n"), 0o644)
		os.WriteFile(filepath.Join(dir, name+".container"), []byte("[Container]\nImage=x\n"), 0o644)
	}
	if err := proposal.CommitDirty(context.Background(), env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}

	id := proposed(call(map[string]any{"action": "einrichten", "geraet": "epson2:net:192.0.2.7"}))
	if _, err := os.Stat(values); err == nil {
		t.Fatal("einrichten installed scan on main: the owner approves a new service")
	}
	p, err := proposal.Get(env.Paths.Home+"/inbox", id)
	if err != nil || p.Class != "human" || p.Kind != "new-service" {
		t.Fatalf("%v: %+v", err, p)
	}

	svc := filepath.Join(env.Paths.State, "services", "scan")
	os.MkdirAll(svc, 0o755)
	os.WriteFile(filepath.Join(svc, "laib.yml"), []byte("name: scan\nprovides: [scan]\nhealth:\n  none: a loop\nstatus:\n  file: ${DATA_DIR}/scan/laib-status.json\n"), 0o644)
	os.WriteFile(filepath.Join(svc, "values.yml"), []byte("SCANNER: \"\"\n"), 0o644)
	os.WriteFile(filepath.Join(svc, "scan.container"), []byte("[Container]\nImage=x\nEnvironment=\"SCANNER=${SCANNER}\"\n"), 0o644)
	if err := proposal.CommitDirty(context.Background(), env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	id = proposed(call(map[string]any{"action": "einrichten", "geraet": "airscan:e0:Brother ADS-4300N"}))
	if b, _ := os.ReadFile(values); string(b) != "SCANNER: \"\"\n" {
		t.Fatalf("einrichten changed values.yml on main: %s", b)
	}
	// a value like any other: the box's dial decides (values: auto applies it, rolls back on red)
	if p, err := proposal.Get(env.Paths.Home+"/inbox", id); err != nil || p.Kind != "values" {
		t.Fatalf("%v: %+v", err, p)
	}
	if out, code := call(map[string]any{"action": "einrichten"}); code == cli.ExitOK {
		t.Fatalf("einrichten without a device on an installed scan: %s", out)
	}
	for _, bad := range []string{"epson2:net:1.2.3.4\nEnvironment=X=1", `a:"b`, "a:%h", "a:$HOME", "nocolon"} {
		if out, code := call(map[string]any{"action": "einrichten", "geraet": bad}); code == cli.ExitOK {
			t.Errorf("%q was proposed: %s", bad, out)
		}
	}

	for _, act := range []string{"suchen", "pruefen", "probe"} {
		if out, code := call(map[string]any{"action": act}); code != cli.ExitOK {
			t.Fatalf("%s: exit %d: %s", act, code, out)
		}
	}
	orders, _ := filepath.Glob(filepath.Join(dataDir, "scan", "auftrag", "*.json"))
	if len(orders) != 3 {
		t.Fatalf("want the three orders, got %v", orders)
	}
}
