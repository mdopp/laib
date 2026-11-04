package proposal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #1036, #215: a template update brings Homes' new MODULE_AUS to a box
// whose values.yml predates it, as the example writes it (empty: every
// module on), and leaves every value set by hand as it was.
func TestATemplateUpdateAddsModuleAus(t *testing.T) {
	example, err := os.ReadFile(filepath.Join("..", "..", "templates", "homes", "values.example.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var old []string
	for _, l := range strings.Split(string(example), "\n") {
		if strings.HasPrefix(l, "MODULE_AUS:") {
			continue
		}
		if strings.HasPrefix(l, "REITTI_URL:") {
			l = "REITTI_URL: http://host.containers.internal:8730 # set by hand"
		}
		old = append(old, l)
	}
	path := filepath.Join(t.TempDir(), "values.yml")
	os.WriteFile(path, []byte(strings.Join(old, "\n")), 0o644)
	if err := newValues(path, example, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "\nMODULE_AUS: \"\"") || !strings.Contains(string(got), "REITTI_URL: http://host.containers.internal:8730 # set by hand\n") || !strings.HasPrefix(string(got), strings.Join(old, "\n")) {
		t.Fatalf("not appended, or a hand-set value touched:\n%s", got)
	}
}
