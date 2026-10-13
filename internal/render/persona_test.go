package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// #125, ADR 0020: the house persona reaches the services as values and as a
// package under render/persona/<name>; a persona change is a change to the
// services that use it, and only to them.
func TestAPersonaChangeTouchesTheServicesThatUseIt(t *testing.T) {
	repo := t.TempDir()
	w := func(p, s string) {
		os.MkdirAll(filepath.Dir(filepath.Join(repo, p)), 0o755)
		os.WriteFile(filepath.Join(repo, p), []byte(s), 0o644)
	}
	w("box.yml", "domain: x.home\ndataDir: /d\nroles: {}\nhostNetwork: []\npersona: hal\nchatPersonas: {\"+4917012345678\": joshua}\n")
	w("policy.yml", "tiers: [read, propose, apply, destroy]\nhands: {human: {ceiling: destroy}}\n")
	c := "name: %s\ntemplate: {registry: builtin, name: %s, version: 1}\nroutes: []\nsecrets: []\ndata: []\nhealth: {none: test}\n"
	w("services/sprecher/laib.yml", strings.ReplaceAll(c, "%s", "sprecher"))
	w("services/sprecher/sprecher.container", "[Container]\nImage=x\nEnvironment=PERSONA=${PERSONA} PERSONA_REV=${PERSONA_REV}\nExec=--wake ${PERSONA_WAKEWORD} --voice ${PERSONA_VOICE}\n")
	w("services/stumm/laib.yml", strings.ReplaceAll(c, "%s", "stumm"))
	w("services/stumm/stumm.container", "[Container]\nImage=y\n")
	w("personas/hal/persona.yml", "name: hal\ndisplay: HAL\nlanguages: [en]\nwakeword: {phrase: Hello Hal}\nvoice: en_US-lessac-medium\n")
	w("personas/hal/style.md", "Calm.\n")
	w("personas/hal/icon.svg", "<svg/>")
	render := func() Output {
		st, err := state.Load(repo)
		if err != nil {
			t.Fatal(err)
		}
		out, err := Render(st, nil)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	a := render()
	unit := a["quadlet/sprecher/sprecher.container"]
	if !strings.Contains(unit, "PERSONA=hal ") || !strings.Contains(unit, "--wake hello_hal --voice en_US-lessac-medium") || strings.Contains(unit, "${PERSONA_REV}") {
		t.Fatalf("persona values in the unit:\n%s", unit)
	}
	if a["persona/hal/style.md"] != "Calm.\n" || a["persona/hal/icon.svg"] == "" || a["persona/joshua/persona.yml"] == "" {
		t.Fatalf("the house and chat personas are rendered under persona/: %v", keys(a))
	}
	if !strings.Contains(a["persona/chat.yml"], "\"+4917012345678\": joshua") {
		t.Fatalf("the chat map is rendered:\n%s", a["persona/chat.yml"])
	}
	w("personas/hal/style.md", "Calm, and a little too calm.\n")
	b := render()
	if a["quadlet/sprecher/sprecher.container"] == b["quadlet/sprecher/sprecher.container"] {
		t.Fatal("a style change does not touch the service that uses the persona")
	}
	if a["quadlet/stumm/stumm.container"] != b["quadlet/stumm/stumm.container"] {
		t.Fatal("a style change touched a service that does not use the persona")
	}
}

// A box where no service uses a persona renders none.
func TestNoPersonaOutputWithoutAUser(t *testing.T) {
	st, err := state.Load("../../testdata/golden/basic/state")
	if err != nil {
		t.Fatal(err)
	}
	out, _ := Render(st, nil)
	for k := range out {
		if strings.HasPrefix(k, "persona/") {
			t.Fatalf("%s rendered on a box without a persona user", k)
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
