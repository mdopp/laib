package apply

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/plan"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// ADR 0027 (#624): on a box that ran the Operator as a host unit, one apply
// stops and disables the native unit and removes it with its env file
// before the reload, hands the token to podman, and starts the container
// that replaces it under the same unit name. A new token alone restarts it.
func TestOneApplyMovesTheOperatorFromTheHostIntoItsContainer(t *testing.T) {
	home := t.TempDir()
	paths := boxfs.Paths{Home: home, State: filepath.Join(home, "state"), Quadlet: filepath.Join(home, "q"), Render: filepath.Join(home, "r"), Systemd: filepath.Join(home, "s"), Lock: filepath.Join(home, "lock")}
	os.MkdirAll(paths.State, 0o755)
	os.WriteFile(filepath.Join(paths.State, "box.yml"), []byte("domain: x.home\n"), 0o644)
	st := &state.State{Box: state.Box{Domain: "x.home", DataDir: filepath.Join(home, "d"), Operator: state.OperatorCfg{Enabled: true}, Model: state.Model{URL: "http://127.0.0.1:11435/v1"}}, Services: map[string]*state.Service{}}
	st.Box.Laib.Port = 5900
	// the box before: the native unit and its env file, owned by laib's manifest
	os.MkdirAll(paths.Systemd, 0o755)
	native := []string{filepath.Join(paths.Systemd, "laib-operator.service"), filepath.Join(paths.Systemd, "laib-operator.env")}
	os.WriteFile(native[0], []byte("[Service]\nExecStart=/var/opt/laib/laib-operator\n[Install]\nWantedBy=default.target\n"), 0o644)
	os.WriteFile(native[1], []byte("LAIB_TOKEN=\"old\"\n"), 0o600)
	if err := plan.WriteManifest(paths, native); err != nil {
		t.Fatal(err)
	}

	var calls []string
	pod := map[string]string{}
	real := sysd.Run
	t.Cleanup(func() { sysd.Run = real })
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		line := name + " " + strings.Join(args, " ")
		calls = append(calls, line)
		switch {
		case name == "git":
			return runGit(t, args...)
		case strings.HasPrefix(line, "podman secret create"):
			pod[args[len(args)-2]] = sysd.Stdin(ctx)
		case strings.HasPrefix(line, "podman secret ls"):
			return strings.Join(slices.Sorted(maps.Keys(pod)), "\n"), nil
		case strings.HasPrefix(line, "podman secret inspect"):
			var out []string
			for _, n := range args[5:] {
				v, _ := json.Marshal(pod[n])
				out = append(out, n+" "+string(v))
			}
			return strings.Join(out, "\n"), nil
		case strings.Contains(line, "is-active"):
			return "active", nil
		}
		return "", nil
	}
	apply := func(sec map[string]string) Result {
		t.Helper()
		calls = nil
		p, err := plan.Compute(context.Background(), st, sec, paths, "")
		if err != nil {
			t.Fatal(err)
		}
		res, err := Run(context.Background(), st, p, paths, sec, "test")
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := apply(map[string]string{render.OperatorToken: "tok-1"})
	at := func(want string) int {
		return slices.IndexFunc(calls, func(c string) bool { return strings.Contains(c, want) })
	}
	reload, stop, disable, start := at("daemon-reload"), at("stop laib-operator.service"), at("disable laib-operator.service"), at("restart laib-operator.service")
	if stop < 0 || disable < 0 || reload < 0 || stop > reload || disable > reload {
		t.Fatalf("the native unit must be stopped and disabled before the reload:\n%s", strings.Join(calls, "\n"))
	}
	if start < reload {
		t.Fatalf("the container must start after the reload:\n%s", strings.Join(calls, "\n"))
	}
	for _, f := range native {
		if _, err := os.Stat(f); err == nil {
			t.Errorf("%s is still on the box", f)
		}
	}
	if _, err := os.Stat(filepath.Join(paths.Quadlet, "laib-operator", "laib-operator.container")); err != nil {
		t.Fatal("the operator's container is not written")
	}
	if pod["laib.laib-operator.laib-operator.LAIB_TOKEN"] != "tok-1" {
		t.Fatalf("the token is no podman secret: %v", pod)
	}
	if !res.Green || !slices.Contains(res.Restarted, "laib-operator.service") || !slices.ContainsFunc(res.Checks, func(c Check) bool { return c.What == "unit laib-operator.service" && c.OK }) {
		t.Fatalf("the operator is not started and verified: %+v", res)
	}
	// a new token alone (enable after disable, #500) restarts the container
	apply(map[string]string{render.OperatorToken: "tok-2"})
	if at("restart laib-operator.service") < 0 || pod["laib.laib-operator.laib-operator.LAIB_TOKEN"] != "tok-2" {
		t.Fatalf("a new token must restart the operator:\n%s", strings.Join(calls, "\n"))
	}
	// nothing changed: the operator is left alone
	apply(map[string]string{render.OperatorToken: "tok-2"})
	if at("restart laib-operator.service") >= 0 {
		t.Fatalf("an unchanged operator was restarted:\n%s", strings.Join(calls, "\n"))
	}
}
