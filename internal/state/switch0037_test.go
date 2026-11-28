package state

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// ADR 0037: box.yml updates.auto is an or aus (unset is on); anything else does not load.
func TestUpdatesAutoIsAnOrAus(t *testing.T) {
	for in, want := range map[string]Switch{"{}": "", "auto: an": "an", "auto: aus": "aus"} {
		var u Updates
		if err := yaml.Unmarshal([]byte(in), &u); err != nil || u.Auto != want {
			t.Errorf("%s: %q %v", in, u.Auto, err)
		}
	}
	var u Updates
	if err := yaml.Unmarshal([]byte("auto: vielleicht"), &u); err == nil {
		t.Fatal("updates.auto: vielleicht loaded")
	}
}
