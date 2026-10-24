package apply

import (
	"context"
	"encoding/json"
	"errors"
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

// #633 (vm-install, logins): laib-notify's container belongs to the box, like
// its timer, not to one service. `laib apply auth` that turns the notifier on
// (SMTP_HOST) or a box that still runs the native notify must get the
// container, the old host unit gone and the timer enabled, in one apply. The
// fake refuses to start a timer whose service the last daemon-reload did not
// load, as systemd does ("Refusing to start, unit … to trigger not loaded").
func TestOneScopedApplyMovesNotifyIntoItsContainerWithTheTimerEnabled(t *testing.T) {
	for _, only := range []string{"", "auth"} {
		t.Run("only="+only, func(t *testing.T) {
			home := t.TempDir()
			paths := boxfs.Paths{Home: home, State: filepath.Join(home, "state"), Quadlet: filepath.Join(home, "q"), Render: filepath.Join(home, "r"), Systemd: filepath.Join(home, "s"), Lock: filepath.Join(home, "lock")}
			os.MkdirAll(paths.State, 0o755)
			os.WriteFile(filepath.Join(paths.State, "box.yml"), []byte("domain: x.home\n"), 0o644)
			st := &state.State{Box: state.Box{Domain: "x.home", DataDir: filepath.Join(home, "d"), Watchdog: state.Watchdog{URL: "https://hc.example/ping"}}, Services: map[string]*state.Service{"auth": {Name: "auth"}}}
			st.Box.Laib.Port = 5900
			// the box before #632: the native oneshot, its env file and the timer
			os.MkdirAll(paths.Systemd, 0o755)
			native := []string{filepath.Join(paths.Systemd, "laib-notify.service"), filepath.Join(paths.Systemd, "laib-notify.env")}
			os.WriteFile(native[0], []byte("[Unit]\nDescription=Laib notify\n\n[Service]\nType=oneshot\nExecStart=/var/opt/laib/laib-notify\n"), 0o644)
			os.WriteFile(native[1], []byte("LAIB_TOKEN=\"old\"\n"), 0o600)
			if err := plan.WriteManifest(paths, native); err != nil {
				t.Fatal(err)
			}

			container := filepath.Join(paths.Quadlet, "laib-notify", "laib-notify.container")
			loaded := false // laib-notify.service after the last daemon-reload
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
				case strings.HasSuffix(line, "daemon-reload"):
					_, errC := os.Stat(container)
					_, errN := os.Stat(native[0])
					loaded = errC == nil || errN == nil
				case strings.Contains(line, "enable --now laib-notify.timer") && !loaded:
					return "Failed to enable unit: Refusing to start, unit laib-notify.service to trigger not loaded.", errors.New("exit status 1")
				case strings.Contains(line, "is-active"):
					return "active", nil
				}
				return "", nil
			}
			sec := map[string]string{render.NotifyToken: "tok-1"}
			p, err := plan.Compute(context.Background(), st, sec, paths, only)
			if err != nil {
				t.Fatal(err)
			}
			res, err := Run(context.Background(), st, p, paths, sec, "test")
			if err != nil {
				t.Fatal(err)
			}
			at := func(want string) int {
				return slices.IndexFunc(calls, func(c string) bool { return strings.Contains(c, want) })
			}
			if reload, enable := at("daemon-reload"), at("enable --now laib-notify.timer"); reload < 0 || enable < reload {
				t.Fatalf("the timer must be enabled after the reload:\n%s", strings.Join(calls, "\n"))
			}
			if _, err := os.Stat(container); err != nil {
				t.Fatalf("the container is not written:\n%s", strings.Join(res.Written, "\n"))
			}
			for _, f := range native {
				if _, err := os.Stat(f); err == nil {
					t.Errorf("%s is still on the box", f)
				}
			}
			if pod["laib.laib-notify.laib-notify.LAIB_TOKEN"] != "tok-1" {
				t.Fatalf("the token is no podman secret: %v", pod)
			}
			if !res.Green || !slices.Contains(res.Restarted, "laib-notify.timer") {
				t.Fatalf("the timer is not enabled: %+v", res.Checks)
			}
		})
	}
}
