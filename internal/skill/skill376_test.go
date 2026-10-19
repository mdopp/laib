package skill

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// #376: a skill declares its mechanics as fields — a cap per tool
// (limits) and, for an updates skill, which level it may propose
// (updates: patch) — and the kernel checks them like the rest.
func TestSkillMechanicsAreFields(t *testing.T) {
	sks := Load(os.DirFS("../../skills"), t.TempDir())
	want := map[string]string{
		"aktualisierung": `"limits":{"propose":3}`,
		"nutzung":        `"limits":{"wish":3}`,
	}
	for _, s := range sks {
		w, ok := want[s.Name]
		if !ok {
			continue
		}
		b, _ := json.Marshal(s)
		if s.Refused != "" || !strings.Contains(string(b), w) {
			t.Errorf("%s: refused %q, want %s in %s", s.Name, s.Refused, w, b)
		}
		if s.Name == "aktualisierung" && !strings.Contains(string(b), `"updates":"patch"`) {
			t.Errorf("aktualisierung does not declare updates: patch: %s", b)
		}
	}
	for yml, why := range map[string]string{
		"description: x\ntrigger: {every: 10m}\ntools: [note]\nlimits: {propose: 3}\nprompt: p\n":             "propose",
		"description: x\ntrigger: {every: 10m}\ntools: [note, propose]\nlimits: {propose: 0}\nprompt: p\n":    "propose",
		"description: x\ntrigger: {every: 10m}\ntools: [note, propose, updates]\nupdates: minor\nprompt: p\n": "updates",
		"description: x\ntrigger: {every: 10m}\ntools: [note]\nupdates: patch\nprompt: p\n":                   "updates",
	} {
		s := parse("x", "state", []byte(yml), nil)
		if s.Refused == "" || !strings.Contains(s.Refused, why) {
			t.Errorf("%q: refused %q, want a reason naming %s", yml, s.Refused, why)
		}
	}
}
