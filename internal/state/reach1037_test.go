package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// perpersonState loads a copy of the perperson golden state, each file
// through edit (nil drops it).
func perpersonState(t *testing.T, edit func(rel string, b []byte) []byte) (*State, error) {
	t.Helper()
	pp, dir := filepath.Join("..", "..", "testdata", "golden", "perperson", "state"), t.TempDir()
	filepath.WalkDir(pp, func(p string, d os.DirEntry, err error) error {
		rel, _ := filepath.Rel(pp, p)
		b, _ := os.ReadFile(p)
		if d.IsDir() || edit(rel, b) == nil {
			return nil
		}
		os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755)
		return os.WriteFile(filepath.Join(dir, rel), edit(rel, b), 0o644)
	})
	return Load(dir)
}

// Ablauf L-54. laib#1037: Homes' REITTI_URL was typed by hand after `laib add reitti`.
// instances: with value: names one plain template's service: its first
// route's address lands in the unit while it is installed and is empty
// without it, and a value set in values.yml wins, so a box that typed it
// keeps working (#215). Adding or removing the service changes the unit.
func TestReachFindsOnePlainServiceAndATypedValueWins(t *testing.T) {
	leser := func(edit func(rel string, b []byte) []byte) string {
		t.Helper()
		st, err := perpersonState(t, edit)
		if err != nil {
			t.Fatal(err)
		}
		return st.Services["leser"].Units["leser.container"]
	}
	same := func(_ string, b []byte) []byte { return b }
	if u := leser(same); !strings.Contains(u, "\nEnvironment=LESER_KARTE=http://host.containers.internal:8495\n") || !strings.Contains(u, "\nEnvironment=LESER_FEHLT=\n") {
		t.Fatalf("installed: its address; not installed: empty:\n%s", u)
	}
	removed := func(rel string, b []byte) []byte {
		if strings.HasPrefix(rel, filepath.Join("services", "karte")) {
			return nil
		}
		return b
	}
	if u := leser(removed); !strings.Contains(u, "\nEnvironment=LESER_KARTE=\n") {
		t.Fatalf("removed: the address is empty again:\n%s", u)
	}
	typed := func(rel string, b []byte) []byte {
		return []byte(strings.Replace(string(b), `KARTE_URL: ""`, "KARTE_URL: http://host.containers.internal:9999", 1))
	}
	if u := leser(typed); !strings.Contains(u, "\nEnvironment=LESER_KARTE=${KARTE_URL}\n") {
		t.Fatalf("a value set by hand wins:\n%s", u)
	}
	for _, bad := range []string{"value: KARTE_URL\n    secret: NOTIZBUCH_KEY\n    env: LESER_KEY2", "value: karte-url"} {
		_, err := perpersonState(t, func(rel string, b []byte) []byte {
			return []byte(strings.Replace(string(b), "value: KARTE_URL", bad, 1))
		})
		if err == nil || !strings.Contains(err.Error(), "instances:") {
			t.Errorf("%q loads: %v", bad, err)
		}
	}
}
