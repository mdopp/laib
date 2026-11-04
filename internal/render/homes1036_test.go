package render

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// #1036 (homes ADR 0001 §5): the owner's switch reaches Homes as
// HOMES_MODULE_AUS, the value as values.yml has it; the shipped example
// leaves it empty, every module whose service runs.
func TestHomesGetsTheModulesLeftOff(t *testing.T) {
	st := shippedBox(t)
	out, err := Render(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out["quadlet/homes/homes.container"], "\nEnvironment=HOMES_MODULE_AUS=\n") {
		t.Fatalf("the example's empty switch:\n%s", out["quadlet/homes/homes.container"])
	}
	values := filepath.Join(st.Dir, "services", "homes", "values.yml")
	b, _ := os.ReadFile(values)
	os.WriteFile(values, regexp.MustCompile(`(?m)^MODULE_AUS: ""`).ReplaceAll(b, []byte("MODULE_AUS: podcasts")), 0o644)
	if st, err = state.Load(st.Dir); err != nil {
		t.Fatal(err)
	}
	if out, err = Render(st, nil); err != nil || !strings.Contains(out["quadlet/homes/homes.container"], "\nEnvironment=HOMES_MODULE_AUS=podcasts\n") {
		t.Fatalf("MODULE_AUS: podcasts (%v):\n%s", err, out["quadlet/homes/homes.container"])
	}
}

// #1036: uses: names every backend of a Homes module, and a box without
// some of them (this one has no gesundheit, whose template lives in its
// own repo) still loads.
func TestHomesUsesEveryBackend(t *testing.T) {
	st := shippedBox(t)
	uses := st.Services["homes"].Contract.Uses
	for _, b := range []string{"home-assistant", "reitti", "radicale", "paperless", "audiobookshelf", "media", "bote", "file-share", "usage-metrics", "gesundheit"} {
		if !slices.Contains(uses, b) {
			t.Errorf("uses: lacks %s: %v", b, uses)
		}
	}
	if st.Services["gesundheit"] != nil {
		t.Fatal("the shipped box has gesundheit: the test proves nothing about a box without it")
	}
}
