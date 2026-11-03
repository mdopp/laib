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

// laib#1021: a renamed service takes its old data dir and secret along
// (`was:`). Apply moves the data and copies the value, so the card says so,
// and a human decides it.
func TestReviewNamesWhatARenamedServiceTakesAlong(t *testing.T) {
	g := filepath.Join("..", "..", "testdata", "golden", "basic", "state")
	base, changed := copyTree(t, g), copyTree(t, g)
	b, err := os.ReadFile(filepath.Join(g, "services", "whoami", "laib.yml"))
	if err != nil {
		t.Fatal(err)
	}
	c := strings.NewReplacer("  - name: WHOAMI_TOKEN\n", "  - name: WHOAMI_TOKEN\n  - name: WHOAMI_KEY\n    was: HALLO_KEY\n",
		"data: []", "data:\n  - path: ${DATA_DIR}/whoami\n    was: ${DATA_DIR}/hallo").Replace(string(b))
	write(t, changed, filepath.Join("services", "whoami", "laib.yml"), c, 0o644)
	got := strings.Join(Dirs(base, changed).Human, "\n")
	for _, want := range []string{"übernimmt das Geheimnis HALLO_KEY als WHOAMI_KEY (whoami)", "übernimmt die Daten aus ${DATA_DIR}/hallo nach ${DATA_DIR}/whoami (whoami)"} {
		if !strings.Contains(got, want) {
			t.Errorf("the review does not say %q: %q", want, got)
		}
	}
}
