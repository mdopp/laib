package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// laib#1016: a service that reaches every person's instance (instances:)
// takes each instance's secret. A new instance hands it one more, another
// service's secret: the owner decides that, as for a !secret in values.yml.
func TestReviewNamesANewInstancesSecretTheReacherTakes(t *testing.T) {
	pp := filepath.Join("..", "..", "testdata", "golden", "perperson", "state")
	base, changed := copyTree(t, pp), copyTree(t, pp)
	for _, f := range []string{"laib.yml", "notizbuch.container", "notizbuch-sync.service", "notizbuch-sync.timer", "values.yml"} {
		b, err := os.ReadFile(filepath.Join(pp, "services", "notizbuch-anna", f))
		if err != nil {
			t.Fatal(err)
		}
		c := strings.NewReplacer("name: notizbuch-anna", "name: notizbuch-lena", "person: anna", "person: lena", "portOffset: 1", "portOffset: 2").Replace(string(b))
		write(t, changed, filepath.Join("services", "notizbuch-lena", f), c, 0o644)
	}
	r := Dirs(base, changed)
	if !strings.Contains(strings.Join(r.Human, "\n"), "leser nutzt das Geheimnis NOTIZBUCH_KEY_LENA") {
		t.Errorf("the review does not name leser taking lena's key: %q", r.Human)
	}
}
