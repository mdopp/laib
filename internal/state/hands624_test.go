package state

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// ADR 0027 (#624, part 2): laib-haushalt and laib-lease call laib back with
// tokens of their own hands. haushalt only proposes; lease reads the
// services, pauses and resumes them and notes a long foreign window —
// nothing else, so a lease that is taken over reaches no further. A
// policy.yml from before has neither: Load supplies them, as notify's.
func TestTheHaushaltAndLeaseHandsAreBuiltInAndSupplied(t *testing.T) {
	want := map[string]Hand{
		"haushalt": {Ceiling: "propose", Verbs: []string{"propose"}},
		"lease":    {Ceiling: "apply", Verbs: []string{"status", "pause", "resume", "note"}},
	}
	for name, w := range want {
		h, ok := defaults.Hands[name]
		if !ok || h.Ceiling != w.Ceiling || !slices.Equal(h.Verbs, w.Verbs) {
			t.Errorf("DefaultPolicy %s: %+v, want %+v", name, h, w)
		}
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "box.yml"), []byte("domain: x.home\ndataDir: /d\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "policy.yml"), []byte("tiers: [read, propose, apply, destroy]\nhands: {human: {ceiling: destroy}}\n"), 0o644)
	st, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	for name, w := range want {
		if h := st.Policy.Hands[name]; h.Ceiling != w.Ceiling || !slices.Equal(h.Verbs, w.Verbs) {
			t.Errorf("a policy.yml without %s gets %+v, want %+v", name, h, w)
		}
	}
}
