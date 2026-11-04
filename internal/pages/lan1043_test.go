package pages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/state"
)

// #1043: laib serve writes the bare address's page from the status; a
// state's since is the write that saw it change, never guessed.
func TestWriteLANKeepsWhenAStateChanged(t *testing.T) {
	st, err := state.Load("../../testdata/golden/nuc/state")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	env := &cli.Env{Paths: boxfs.Paths{Home: home, Render: filepath.Join(home, "render")}}
	rows := func(auth string) []ServiceStatus {
		var out []ServiceStatus
		for _, n := range st.ServiceNames() {
			r := ServiceStatus{Name: n, State: "grün"}
			if n == "auth" {
				r.State = auth
			}
			out = append(out, r)
		}
		return out
	}
	read := func() string {
		b, err := os.ReadFile(filepath.Join(home, "render", "status", "index.html"))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	auth := func(page string) string {
		_, s, _ := strings.Cut(page, "<b>auth</b>")
		s, _, _ = strings.Cut(s, "</p>")
		return s
	}
	t1 := time.Date(2026, 11, 4, 8, 0, 0, 0, time.Local)
	if err := WriteLAN(env, st, Readiness{Version: "2.1.0"}, rows("grün"), t1, 3*time.Hour); err != nil {
		t.Fatal(err)
	}
	p := read()
	for _, n := range st.ServiceNames() {
		if !strings.Contains(p, "<b>"+n+"</b>") {
			t.Errorf("%s is not on the written page", n)
		}
	}
	if strings.Contains(p, "seit 04.11.") || !strings.Contains(p, "Box läuft seit 3 Stunden") || !strings.Contains(p, "Laib 2.1.0") {
		t.Errorf("first write: no since is known yet, the uptime and version are: %s", p)
	}
	t2 := t1.Add(2 * time.Minute)
	WriteLAN(env, st, Readiness{}, rows("rot"), t2, 0)
	if a := auth(read()); !strings.Contains(a, "rot</span>") || !strings.Contains(a, "seit 04.11. 08:02") {
		t.Errorf("auth went red at 08:02, its line says: %q", a)
	}
	WriteLAN(env, st, Readiness{}, rows("rot"), t2.Add(4*time.Minute), 0)
	if a := auth(read()); !strings.Contains(a, "seit 04.11. 08:02") {
		t.Errorf("still red: the since stays 08:02, the line says %q", a)
	}
	if strings.Contains(read(), "<b>whoami</b> <span class=state><span class=dot></span>grün</span> <small") {
		t.Error("whoami never changed: no since")
	}
}
