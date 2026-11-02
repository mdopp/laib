package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// ADR 0030: the assistant's name is one box.yml value, "Homes" when unset;
// it reaches a unit as ${ASSISTANT}, and nothing is rendered beside it.
func TestTheAssistantsNameReachesTheUnitsThatUseIt(t *testing.T) {
	for box, want := range map[string]string{"": "Homes", "assistant: Hal\n": "Hal"} {
		repo := t.TempDir()
		w := func(p, s string) {
			os.MkdirAll(filepath.Dir(filepath.Join(repo, p)), 0o755)
			os.WriteFile(filepath.Join(repo, p), []byte(s), 0o644)
		}
		w("box.yml", "domain: x.home\ndataDir: /d\nroles: {}\nhostNetwork: []\n"+box)
		w("policy.yml", "tiers: [read, propose, apply, destroy]\nhands: {human: {ceiling: destroy}}\n")
		w("services/sprecher/laib.yml", "name: sprecher\ntemplate: {registry: builtin, name: sprecher, version: 1}\nroutes: []\nsecrets: []\ndata: []\nhealth: {none: test}\n")
		w("services/sprecher/sprecher.container", "[Container]\nImage=x\nEnvironment=NAME=${ASSISTANT}\n")
		st, err := state.Load(repo)
		if err != nil {
			t.Fatal(err)
		}
		out, err := Render(st, nil)
		if err != nil {
			t.Fatal(err)
		}
		if unit := out["quadlet/sprecher/sprecher.container"]; !strings.Contains(unit, "\nEnvironment=NAME="+want+"\n") {
			t.Errorf("box.yml %q: the unit names %q:\n%s", box, want, unit)
		}
		for k := range out {
			if strings.HasPrefix(k, "persona/") {
				t.Errorf("rendered %s: there are no personas (ADR 0030)", k)
			}
		}
	}
}

func keys(o Output) []string {
	var k []string
	for p := range o {
		k = append(k, p)
	}
	return k
}
