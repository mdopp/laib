package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// laib#1016: instances: names a .container of the service, an environment
// name for the addresses, and a secret with its env or neither; a secret
// the instances do not declare fails the load.
func TestReachRefusesWhatItCannotWire(t *testing.T) {
	pp := filepath.Join("..", "..", "testdata", "golden", "perperson", "state")
	for bad, want := range map[string]string{
		"unit: leser.container":   "unit: leser-sync.service",
		"url: LESER_NOTIZBUECHER": "url: leser-notizbuecher",
		"env: LESER_KEY":          "env: \"\"",
		"secret: NOTIZBUCH_KEY":   "secret: NOTIZBUCH_PIN",
	} {
		dir := t.TempDir()
		filepath.WalkDir(pp, func(p string, d os.DirEntry, err error) error {
			rel, _ := filepath.Rel(pp, p)
			if d.IsDir() {
				return os.MkdirAll(filepath.Join(dir, rel), 0o755)
			}
			b, _ := os.ReadFile(p)
			if rel == filepath.Join("services", "leser", "laib.yml") {
				b = []byte(strings.Replace(string(b), bad, want, 1))
			}
			return os.WriteFile(filepath.Join(dir, rel), b, 0o644)
		})
		if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "instances:") {
			t.Errorf("%s loads: %v", want, err)
		}
	}
}
