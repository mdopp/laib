package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/backup"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// #444: the nightly backup was refused on every box ("laib may not run a
// backup"): the rendered units run as LAIB_ACTOR=laib, and laib init's
// policy.yml had no hand laib. The box's own timers run the rendered command
// line here, actor and all, against a fresh `laib init` state, and against
// one written before the fix (no laib hand: the Kraftwerk's).
func TestTheRenderedTimersRunAsTheirActor(t *testing.T) {
	for _, before444 := range []bool{false, true} {
		home := t.TempDir()
		t.Setenv("LAIB_HOME", home)
		t.Setenv("LAIB_QUADLET", filepath.Join(home, "quadlet"))
		t.Setenv("LAIB_SYSTEMD", filepath.Join(home, "systemd"))
		real := sysd.Run
		t.Cleanup(func() { sysd.Run = real })
		sysd.Run = func(_ context.Context, name string, args ...string) (string, error) {
			if name == "git" {
				out, err := exec.Command("git", args...).CombinedOutput()
				return string(out), err
			}
			return "", nil
		}
		t.Setenv("LAIB_ACTOR", "human:test")
		if rc := runArgs([]string{"init", "--domain", "e2e.home", "--data-dir", filepath.Join(home, "data", "stacks")}); rc != cli.ExitOK {
			t.Fatalf("init rc %d", rc)
		}
		pol := filepath.Join(home, "state", "policy.yml")
		if before444 {
			raw, _ := os.ReadFile(pol)
			os.WriteFile(pol, []byte(regexp.MustCompile(`(?m)^  laib: .*\n`).ReplaceAllString(string(raw), "")), 0o644)
		}
		st, err := state.Load(filepath.Join(home, "state"))
		if err != nil {
			t.Fatal(err)
		}
		out, err := render.Render(st, nil)
		if err != nil {
			t.Fatal(err)
		}
		realRestic := backup.ResticRun
		t.Cleanup(func() { backup.ResticRun = realRestic })
		var snapshots []string
		backup.ResticRun = func(_ context.Context, st *state.State, _ string, args ...string) (string, error) {
			if args[0] == "backup" {
				snapshots = append(snapshots, st.Box.Backup.Repo)
				return "snapshot 0a1b2c3d saved", nil
			}
			if args[0] == "snapshots" {
				return `[{"id":"0a1b2c3d4e5f"}]`, nil
			}
			return "", nil
		}
		for _, unit := range []string{"systemd/laib-backup.service", "systemd/podman-auto-update.service.d/laib.conf"} {
			text := out[unit]
			actor := regexp.MustCompile(`(?m)^Environment=LAIB_ACTOR=(.*)$`).FindStringSubmatch(text)
			cmd := regexp.MustCompile(`(?m)^Exec(?:Start|StartPre)=/var/opt/laib/laib (backup.*)$`).FindStringSubmatch(text)
			if actor == nil || cmd == nil {
				t.Fatalf("%s: no laib backup line with its actor:\n%s", unit, text)
			}
			t.Setenv("LAIB_ACTOR", actor[1])
			snapshots = nil
			if rc := runArgs(strings.Fields(cmd[1])); rc != cli.ExitOK || len(snapshots) != 1 || snapshots[0] != st.Box.Backup.Repo {
				t.Fatalf("before444=%v %s as %s: rc %d, snapshots %v", before444, unit, actor[1], rc, snapshots)
			}
		}
		// the monthly restore test runs as the same actor
		t.Setenv("LAIB_ACTOR", "laib")
		if rc := runArgs([]string{"backup", "check"}); rc != cli.ExitOK || !strings.Contains(out["systemd/laib-backup-check.service"], "ExecStart=/var/opt/laib/laib backup check\nEnvironment=LAIB_HOME=/mnt/data/laib\nEnvironment=LAIB_ACTOR=laib\n") {
			t.Fatalf("before444=%v: backup check as laib: rc %d", before444, rc)
		}
		es, _ := journal.Read(filepath.Join(home, "journal"), time.Time{})
		var green int
		for _, e := range es {
			if e.Verb == "backup" && e.Result == "green" && e.Actor == "laib" {
				green++
			}
		}
		if green != 3 {
			t.Fatalf("before444=%v: two snapshots and a check, journaled green as laib: %+v", before444, es)
		}
	}
}
