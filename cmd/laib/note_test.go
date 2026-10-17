package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// The box.yml editors behind `laib operator enable|disable`: the rest of the
// file — comments included — survives, a missing block is appended, an
// existing key is overwritten in place.
func TestSetBoxKeepsTheRest(t *testing.T) {
	const bare = "# the box\ndomain: home.example # public name\ndataDir: /mnt/data/stacks\n"
	const withBlocks = "# the box\ndomain: home.example\noperator:\n    enabled: false # off for now\n    model: small\nnotify:\n    # the webhook\n    url: https://hooks.example/x\n"
	edit := func(t *testing.T, in string, f func(string) error) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "box.yml")
		os.WriteFile(p, []byte(in), 0o644)
		if err := f(p); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(p)
		return string(b)
	}
	for _, c := range []struct {
		name, in, want string
		f              func(string) error
	}{
		{"operator into bare", bare, "# the box\ndomain: home.example # public name\ndataDir: /mnt/data/stacks\noperator:\n    enabled: true\n    model: big\n    reportAt: 07:00\n",
			func(p string) error {
				return state.SetBlock(p, "operator", "enabled", "true", "model", "big", "reportAt", "07:00")
			}},
		{"operator over existing", withBlocks, "# the box\ndomain: home.example\noperator:\n    enabled: true # off for now\n    model: big\n    reportAt: 07:00\nnotify:\n    # the webhook\n    url: https://hooks.example/x\n",
			func(p string) error {
				return state.SetBlock(p, "operator", "enabled", "true", "model", "big", "reportAt", "07:00")
			}},
		{"operator disable keeps model", withBlocks, "# the box\ndomain: home.example\noperator:\n    enabled: false # off for now\n    model: small\nnotify:\n    # the webhook\n    url: https://hooks.example/x\n",
			func(p string) error {
				return state.SetBlock(p, "operator", "enabled", "false", "model", "", "reportAt", "")
			}},
		{"notify into bare", bare, "# the box\ndomain: home.example # public name\ndataDir: /mnt/data/stacks\nnotify:\n    url: http://127.0.0.1:5901/event\n",
			func(p string) error { return state.SetBlock(p, "notify", "url", "http://127.0.0.1:5901/event") }},
		{"notify over existing", withBlocks, "# the box\ndomain: home.example\noperator:\n    enabled: false # off for now\n    model: small\nnotify:\n    # the webhook\n    url: http://127.0.0.1:5901/event\n",
			func(p string) error { return state.SetBlock(p, "notify", "url", "http://127.0.0.1:5901/event") }},
	} {
		if got := edit(t, c.in, c.f); got != c.want {
			t.Errorf("%s:\n%s\nwant:\n%s", c.name, got, c.want)
		}
	}
}
