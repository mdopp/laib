package render

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// #1024: a box's bote, haus and home-assistant from before ADR 0030 name
// the retired persona builtins; their template updates arrive only once
// 1.x runs. 1.x loads that state and renders those units as 0.60.1 did
// (the lines below are 0.60.1's render of the box's state), so the
// self-update changes nothing for them. Without it every 1.x self-update
// met a state it could not load and was taken back.
func TestAStateFromBeforeADR0030LoadsAndRendersAs0xDid(t *testing.T) {
	repo := t.TempDir()
	w := func(p, s string) {
		os.MkdirAll(filepath.Dir(filepath.Join(repo, p)), 0o755)
		os.WriteFile(filepath.Join(repo, p), []byte(s), 0o644)
	}
	w("box.yml", "domain: x.home\ndataDir: /d\nroles: {}\nhostNetwork: []\n")
	w("policy.yml", "tiers: [read, propose, apply, destroy]\nhands: {human: {ceiling: destroy}}\n")
	for _, name := range []string{"bote", "haus", "home-assistant"} {
		w("services/"+name+"/laib.yml", "name: "+name+"\ntemplate: {registry: builtin, name: "+name+", version: 1}\nroutes: []\nsecrets: []\ndata: []\nhealth: {none: test}\n")
	}
	// the units' lines as the box's state has them (templates of 0.60.1)
	w("services/bote/bote-persona.container", "[Container]\nImage=x\nEnvironment=PERSONA=${PERSONA}\nEnvironment=PERSONA_REV=${PERSONA_REV}\nVolume=/mnt/data/laib/render/persona:/personas:ro,z\n")
	w("services/haus/haus.container", "[Container]\nImage=x\nEnvironment=HAUS_PERSONA=${PERSONA}\nEnvironment=PERSONA_REV=${PERSONA_REV}\nVolume=${DATA_DIR}/stimme/recordings/${PERSONA_WAKEWORD}/haus:/stimme-aufnahmen\n")
	w("services/home-assistant/home-assistant-check.service", "[Service]\nEnvironment=VOICE_PERSONA=${PERSONA_DISPLAY}\nEnvironment=VOICE_WAKEWORD=${PERSONA_WAKEWORD}\nEnvironment=\"VOICE_PHRASE=${PERSONA_PHRASE}\"\n")
	st, err := state.Load(repo)
	if err != nil {
		t.Fatalf("a state from before ADR 0030 must load: %v", err)
	}
	out, err := Render(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	for file, lines := range map[string][]string{
		"quadlet/bote/bote-persona.container":  {"Environment=PERSONA=joshua", "Environment=PERSONA_REV=e3a0b0524430"},
		"quadlet/haus/haus.container":          {"Environment=HAUS_PERSONA=joshua", "Environment=PERSONA_REV=e3a0b0524430", "Volume=/d/stimme/recordings/hey_joshua/haus:/stimme-aufnahmen"},
		"systemd/home-assistant-check.service": {"Environment=VOICE_PERSONA=Joshua", "Environment=VOICE_WAKEWORD=hey_joshua", "Environment=\"VOICE_PHRASE=Hey Joshua\""},
	} {
		for _, l := range lines {
			if !strings.Contains(out[file], "\n"+l+"\n") {
				t.Errorf("%s lacks %q, as 0.60.1 rendered it:\n%s", file, l, out[file])
			}
		}
	}
}

// ADR 0021 §8: new code never takes a legacy name on. No builtin template
// names a retired persona builtin; the legacy table ends once no service on
// a box does.
func TestNoTemplateNamesARetiredPersonaBuiltin(t *testing.T) {
	filepath.WalkDir(filepath.Join("..", "..", "templates"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := os.ReadFile(p)
		for v := range state.LegacyBuiltins {
			if strings.Contains(string(b), "${"+v+"}") {
				t.Errorf("%s names ${%s}, retired by ADR 0030: ${ASSISTANT}", p, v)
			}
		}
		return nil
	})
}
