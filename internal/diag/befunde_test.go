package diag

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/state"
	"gopkg.in/yaml.v3"
)

// laib befunde (owner 2026-11-06): Homes reads the last whole diag run, red
// and gelb with its time, and the read leaves diag.json as it was. Before
// the first run it says so: at null, no findings.
func TestBefundeReadsTheLastRunAndChangesNothing(t *testing.T) {
	home := t.TempDir()
	stand := func() (at *time.Time, fs []Finding) {
		var out bytes.Buffer
		env := &cli.Env{JSON: true, Out: &out}
		env.Paths.Home = home
		if code := Stand(context.Background(), env, nil); code != 0 {
			t.Fatalf("exit %d", code)
		}
		var k struct {
			At       *time.Time `json:"at"`
			Findings []Finding  `json:"findings"`
		}
		if err := json.Unmarshal(out.Bytes(), &k); err != nil || k.Findings == nil {
			t.Fatalf("%v: %s", err, out.String())
		}
		return k.At, k.Findings
	}
	if at, fs := stand(); at != nil || len(fs) != 0 {
		t.Fatalf("not run yet: %v %v", at, fs)
	}
	when := time.Date(2026, 11, 6, 9, 30, 0, 0, time.UTC)
	keep(home, []Finding{{Severity: "rot", Probe: "raid", Detail: "md127: 1 von 2"}, {Severity: "gelb", Probe: "boot", Detail: "/boot knapp"}}, when)
	before, _ := os.ReadFile(keptPath(home))
	at, fs := stand()
	if at == nil || !at.Equal(when) || len(fs) != 2 || fs[0].Severity != "rot" || fs[1].Severity != "gelb" {
		t.Fatalf("%v %+v", at, fs)
	}
	if after, _ := os.ReadFile(keptPath(home)); !bytes.Equal(before, after) {
		t.Fatal("befunde rewrote diag.json")
	}
}

// The homes hand may read befunde, and still not diag: a full run rewrites
// diag.json and closes Box-Aufgaben.
func TestHomesMayReadBefundeButNotRunDiag(t *testing.T) {
	var p state.Policy
	if err := yaml.Unmarshal([]byte(state.DefaultPolicy), &p); err != nil {
		t.Fatal(err)
	}
	if !policy.MayVerb(p, "homes", "befunde") || policy.MayVerb(p, "homes", "diag") {
		t.Fatalf("homes verbs: %v", p.Hands["homes"].Verbs)
	}
}
