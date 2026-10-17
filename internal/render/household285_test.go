package render

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// #285, owner 2026-10-17: Qwen is the household model. Every consumer's
// default is the llama template's household preset, so nobody pulls the
// card away from it: the Operator, Frage, the Bote and Haus.
func TestEveryConsumerDefaultsToTheHouseholdPreset(t *testing.T) {
	root := filepath.Join("..", "..")
	var presets struct {
		Household string
		Presets   []struct {
			Alias string
			Ctx   int
		}
	}
	b, err := os.ReadFile(filepath.Join(root, "templates", "llama", "presets.json"))
	if err == nil {
		err = json.Unmarshal(b, &presets)
	}
	if err != nil {
		t.Fatal(err)
	}
	h := presets.Household
	if !strings.HasPrefix(h, "qwen3") {
		t.Fatalf("the household preset is %q, not Qwen 3.x", h)
	}
	unit := operatorUnit(&state.State{}, nil)
	if !strings.Contains(unit, "Environment=MODEL_NAME="+h+"\n") {
		t.Fatalf("the Operator's unit does not default to %s:\n%s", h, unit)
	}
	for file, re := range map[string]string{
		"templates/haus/haus.container":      `HAUS_LLM_MODEL=(\S+)`,
		"templates/bote/values.example.yml":  `(?m)^MODEL_NAME: (\S+)`,
		"templates/frage/values.example.yml": `(?m)^MODEL_NAME: (\S+)`,
		"cmd/frage/main.go":                  `"MODEL_NAME", "([^"]+)"`,
		"operator/cmd/laib-operator/main.go": `"MODEL_NAME", "([^"]+)"`,
	} {
		b, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		m := regexp.MustCompile(re).FindSubmatch(b)
		if m == nil || string(m[1]) != h {
			t.Errorf("%s defaults to %q, not the household preset %s", file, m, h)
		}
	}
}
