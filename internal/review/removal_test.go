package review

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Gesundheit's retirement (2026-11-06): a removed service is named, and the
// card says what becomes of its data — on the disk always, in the backup only
// while box.yml backup.keep names its folder.
func TestReviewSaysWhereARemovedServicesDataStay(t *testing.T) {
	base := copyTree(t, golden)
	edit(t, base, "services/whoami/laib.yml", "data: []\n", "data:\n  - path: ${DATA_DIR}/whoami\n")
	gone := copyTree(t, base)
	removeAll(t, gone, "services/whoami")
	r := Dirs(base, gone)
	if !reflect.DeepEqual(r.Removed, []string{"whoami"}) || !strings.Contains(strings.Join(r.Human, " "), "entfernt den Dienst whoami") {
		t.Fatalf("removed %v, human %q", r.Removed, r.Human)
	}
	if facts := strings.Join(r.Facts, " "); !strings.Contains(facts, "bleiben auf der Platte, werden aber nicht mehr gesichert") {
		t.Errorf("facts %q", facts)
	}
	kept := copyTree(t, gone)
	edit(t, kept, "box.yml", "  schedule: \"03:30\"\n", "  schedule: \"03:30\"\n  keep: [\"${DATA_DIR}/whoami\"]\n")
	r = Dirs(base, kept)
	if facts := strings.Join(r.Facts, " "); !strings.Contains(facts, "bleiben auf der Platte und werden weiter gesichert") {
		t.Errorf("kept: facts %q, human %q", facts, r.Human)
	}
	if !strings.Contains(strings.Join(r.Human, " "), "Sicherung") {
		t.Errorf("a box.yml backup change is a human's: %q", r.Human)
	}
}

func removeAll(t *testing.T, dir, rel string) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(dir, rel)); err != nil {
		t.Fatal(err)
	}
}
