package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/pages"
	"github.com/mdopp/laib/internal/sysd"
)

// #368: an OS update is the normal path on the GPU box. Zincati stages it
// and asks laib before it reboots (Zincati's fleet_lock strategy, laib is
// the lock): laib grants the reboot only in the update window, which never
// shares the backup slot (backup.schedule, default 03:00: the window is two
// to four hours after it), never while the backup runs, never while laib is
// red, and never before the running deployment is proven by the boot check.
// The box says when an update waits (diag) and when one was let through and
// arrived (journal).

const (
	bootedSum = "38aa0767aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	stagedOS  = `{"deployments":[{"booted":false,"staged":true,"checksum":"5151deadbeef","version":"44.20261101.3.0"},{"booted":true,"staged":false,"checksum":"` + bootedSum + `","version":"44.20260928.3.1"}]}`
)

// osBox is a cbox whose rpm-ostree reports a staged update, whose booted
// deployment is proven, and whose backup is not running.
func osBox(t *testing.T) *cbox {
	t.Helper()
	c := newCbox(t)
	etc := t.TempDir()
	t.Setenv("LAIB_ETC", etc)
	os.MkdirAll(filepath.Join(etc, "laib"), 0o755)
	// the boot check's records live in /var/lib/laib, shared by every deployment (train 22)
	vardir := t.TempDir()
	t.Setenv("LAIB_VAR", vardir)
	os.WriteFile(filepath.Join(vardir, "boot-proven"), []byte(bootedSum+" 0 0\n"), 0o644)
	c.down["laib-backup.service"] = true
	c.down["laib-images.service"] = true
	prev := sysd.Run
	t.Cleanup(func() { sysd.Run = prev })
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "rpm-ostree" && slices.Contains(args, "--json") {
			return stagedOS, nil
		}
		return prev(ctx, name, args...)
	}
	return c
}

// at is a local time on some day, as the clock in the window test reads it.
func at(hhmm string) func() time.Time {
	return func() time.Time {
		t, _ := time.ParseInLocation("2006-01-02 15:04", "2026-10-20 "+hhmm, time.Local)
		return t
	}
}

func preReboot(t *testing.T, c *cbox, now func() time.Time, remote string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/pre-reboot", strings.NewReader(`{"client_params":{"id":"kraftwerk","group":"default"}}`))
	req.Header.Set("fleet-lock-protocol", "true")
	req.RemoteAddr = remote
	rec := httptest.NewRecorder()
	pages.FleetLock(c.env, now)(rec, req)
	return rec.Code
}

func osJournal(t *testing.T, c *cbox) []journal.Entry {
	es, _ := journal.Read(c.env.Paths.Journal, time.Time{})
	var out []journal.Entry
	for _, e := range es {
		if e.Verb == "os-update" {
			out = append(out, e)
		}
	}
	return out
}

func TestTheRebootIsGrantedInTheWindowWhenAllIsGreen(t *testing.T) {
	c := osBox(t)
	if code := preReboot(t, c, at("06:00"), "127.0.0.1:41000"); code != http.StatusOK {
		t.Fatalf("06:00, green, proven, no backup: want the lock (200), got %d", code)
	}
	if es := osJournal(t, c); len(es) != 1 || !strings.Contains(es[0].Detail, "44.20261101.3.0") {
		t.Fatalf("the grant must be journalled with the version it lets through: %+v", es)
	}
}

func TestTheRebootIsHeld(t *testing.T) {
	cases := map[string]func(c *cbox) (func() time.Time, string){
		"in the backup slot": func(c *cbox) (func() time.Time, string) { return at("03:20"), "127.0.0.1:1" },
		"at midday":          func(c *cbox) (func() time.Time, string) { return at("12:00"), "127.0.0.1:1" },
		"while the backup runs": func(c *cbox) (func() time.Time, string) {
			delete(c.down, "laib-backup.service")
			return at("06:00"), "127.0.0.1:1"
		},
		// #547: Laib's own update and the images run in laib-images.service; a reboot must not cut them
		"while the nightly update runs": func(c *cbox) (func() time.Time, string) {
			delete(c.down, "laib-images.service")
			return at("06:00"), "127.0.0.1:1"
		},
		"while a service is red": func(c *cbox) (func() time.Time, string) {
			c.down["whoami.service"] = true
			return at("06:00"), "127.0.0.1:1"
		},
		"before the running deployment is proven": func(c *cbox) (func() time.Time, string) {
			os.WriteFile(filepath.Join(os.Getenv("LAIB_VAR"), "boot-proven"), []byte("ca311262 0 0\n"), 0o644)
			return at("06:00"), "127.0.0.1:1"
		},
		"from the LAN": func(c *cbox) (func() time.Time, string) { return at("06:00"), "192.168.1.20:5000" },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			c := osBox(t)
			now, remote := setup(c)
			if code := preReboot(t, c, now, remote); code == http.StatusOK {
				t.Fatalf("the reboot lock was granted %s", name)
			}
			if es := osJournal(t, c); len(es) != 0 {
				t.Fatalf("a held reboot must not fill the journal (Zincati asks every few minutes): %+v", es)
			}
		})
	}
}

func TestTheArrivedUpdateIsJournalled(t *testing.T) {
	c := osBox(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/steady-state", strings.NewReader(`{"client_params":{"id":"kraftwerk","group":"default"}}`))
	req.Header.Set("fleet-lock-protocol", "true")
	req.RemoteAddr = "127.0.0.1:1"
	rec := httptest.NewRecorder()
	pages.FleetLock(c.env, at("05:40"))(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("steady-state: %d", rec.Code)
	}
	if es := osJournal(t, c); len(es) != 1 || !strings.Contains(es[0].Detail, "44.20260928.3.1") {
		t.Fatalf("the running OS version must be journalled at steady state: %+v", es)
	}
}

func TestTheWindowFollowsTheBackupSchedule(t *testing.T) {
	c := osBox(t)
	st, _ := cli.LoadState(c.env)
	for sched, want := range map[string]string{"03:00": "05:00–07:00", "23:30": "01:30–03:30", "": "05:00–07:00"} {
		st.Box.Backup.Schedule = sched
		if got := diag.WindowText(st); got != want {
			t.Errorf("backup at %q: window %s, want %s", sched, got, want)
		}
	}
	st.Box.Backup.Schedule = "23:30"
	for hhmm, in := range map[string]bool{"23:45": false, "01:30": true, "03:29": true, "03:30": false} {
		if diag.InWindow(st, at(hhmm)()) != in {
			t.Errorf("backup at 23:30, %s: in window %v, want %v", hhmm, !in, in)
		}
	}
}

func TestDiagSaysAnOSUpdateWaits(t *testing.T) {
	c := osBox(t)
	st, _ := cli.LoadState(c.env)
	for _, f := range diag.Probes(context.Background(), c.env, st, "") {
		if f.Probe == "os-update" {
			if !strings.Contains(f.Detail, "44.20261101.3.0") || !strings.Contains(f.Detail, diag.WindowText(st)) {
				t.Fatalf("the finding must name the version and the window: %+v", f)
			}
			return
		}
	}
	t.Fatal("a staged OS update is not in diag")
}
