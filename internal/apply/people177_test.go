package apply

import (
	"os"
	"path/filepath"
	"testing"
)

// #177: render/people/people.json names every person's numbers; it is
// written for the box's user only, like a unit with a secret.
func TestPeopleJSONIsPrivate(t *testing.T) {
	dir := t.TempDir()
	for file, want := range map[string]os.FileMode{
		"render/people/people.json": 0o600,
		"render/persona/chat.yml":   0o644,
	} {
		p := filepath.Join(dir, file)
		if err := writeAtomic(p, "{}\n"); err != nil {
			t.Fatal(err)
		}
		if info, _ := os.Stat(p); info.Mode().Perm() != want {
			t.Errorf("%s: mode %v, want %v", file, info.Mode().Perm(), want)
		}
	}
}
