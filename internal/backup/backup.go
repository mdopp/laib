// Package backup is restic for the box (#309): `laib backup` and its
// monthly check, `laib restore`, the pre-repair snapshot's prep and the
// recovery sheet. Trust kernel: snapshots fail closed (ADR 0019 §2c).
package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// ResticPassword is the secret name; generated on first backup, print it and
// keep it with the age key.
const ResticPassword = "RESTIC_PASSWORD"

func restic(ctx context.Context, st *state.State, pw string, args ...string) (string, error) {
	// inside podman's user namespace (rootless): the services' files belong
	// to subuids of core (a database's pgdata, mode 0700) that core itself
	// cannot read, and a restore must give them their owners back
	// (vm-install run 38308352527). Without podman (a laptop) restic runs as is.
	name, argv := "restic", args
	if _, err := exec.LookPath("podman"); err == nil {
		name, argv = "podman", append([]string{"unshare", "restic"}, args...)
	}
	cmd := exec.CommandContext(ctx, name, argv...)
	cmd.Env = append(os.Environ(), "RESTIC_REPOSITORY="+st.Box.Backup.Repo, "RESTIC_PASSWORD="+pw)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return text, fmt.Errorf("restic %s: %s", args[0], text)
	}
	return text, nil
}

// ResticRun is the seam tests swap; it defaults to the real binary.
var ResticRun = restic

func Prep(env *cli.Env) (*state.State, string, error) {
	st, err := cli.LoadState(env)
	if err != nil {
		return nil, "", err
	}
	if st.Box.Backup.Repo == "" {
		return nil, "", errors.New("box.yml has no backup.repo")
	}
	sec, err := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	if err != nil {
		return nil, "", err
	}
	pw, ok := sec[ResticPassword]
	if fromSheet := os.Getenv("RESTIC_PASSWORD"); fromSheet != "" {
		// the password from the recovery sheet (a fresh box, #40): it is the
		// truth, and it goes into secrets.age so the next run has it
		pw, ok = fromSheet, true
		if sec[ResticPassword] != fromSheet {
			sec[ResticPassword] = fromSheet
			if err := secrets.Save(env.Paths.Secrets, env.Paths.Key, sec); err != nil {
				return nil, "", err
			}
		}
	}
	if !ok {
		if repoExists(context.Background(), st) {
			// never invent a password for a repo that has one: a restore would
			// fail with "wrong password" instead of asking for the old one (#40)
			return nil, "", fmt.Errorf("the repo %s exists but RESTIC_PASSWORD is not in secrets.age: the password from the recovery sheet — RESTIC_PASSWORD=… laib restore --box latest, or laib secret set RESTIC_PASSWORD", st.Box.Backup.Repo)
		}
		pw = secrets.Generate()
		sec[ResticPassword] = pw
		if err := secrets.Save(env.Paths.Secrets, env.Paths.Key, sec); err != nil {
			return nil, "", err
		}
		fmt.Fprintln(env.Errw(), "laib: RESTIC_PASSWORD generated — `laib recovery-sheet` prints it with the age key; print that page")
	}
	return st, pw, nil
}

// repoExists: a local repo has its config file; a remote one answers
// `restic cat config` with "wrong password" when it exists and with
// "repository does not exist" when it does not.
func repoExists(ctx context.Context, st *state.State) bool {
	repo := st.Box.Backup.Repo
	if repo == "" {
		return false
	}
	if strings.HasPrefix(repo, "/") {
		_, err := os.Stat(filepath.Join(repo, "config"))
		return err == nil
	}
	out, err := ResticRun(ctx, st, "x", "cat", "config")
	if err == nil {
		return true
	}
	return !strings.Contains(strings.ToLower(out+" "+err.Error()), "does not exist")
}

func Cmd(ctx context.Context, env *cli.Env, args []string) int {
	st, pw, err := Prep(env)
	if err != nil {
		return env.Fail(err)
	}
	if !policy.Allowed(st.Policy, proposal.Hand(env.Actor), "propose") {
		return cli.Refused(env, env.Actor+" may not run a backup")
	}
	if len(args) > 0 && args[0] == "check" {
		return backupCheck(ctx, env, st, pw)
	}
	if _, err := ResticRun(ctx, st, pw, "cat", "config"); err != nil {
		if _, err := ResticRun(ctx, st, pw, "init"); err != nil {
			journal.Append(env.Paths.Journal, journal.Entry{Actor: env.Actor, Verb: "backup", Result: "red", Detail: err.Error()})
			return env.Fail(err)
		}
	}
	paths := render.BackupPaths(st, env.Paths.State, env.Paths.Journal)
	// quiesce: true paths (databases, #40) are snapshotted with their service
	// stopped — under the lock and paused, so converge keeps its hands off
	unlock, code := apply.TakeLock(env, st, "backup")
	if code != cli.ExitOK {
		return code
	}
	defer unlock()
	var quiesced []string
	for _, name := range st.Order(st.ServiceNames()) {
		for _, d := range st.Services[name].Contract.Data {
			if d.Quiesce && (d.Backup == nil || *d.Backup) {
				quiesced = append(quiesced, name)
				break
			}
		}
	}
	for i := len(quiesced) - 1; i >= 0; i-- { // dependents first
		name := quiesced[i]
		apply.WritePause(env.Paths, apply.Pause{Service: name, By: "backup:" + env.Actor, Until: time.Now().Add(time.Hour).UTC(), Reason: "quiesce"})
		for _, u := range st.Services[name].UnitNames() {
			sysd.Stop(ctx, u)
		}
	}
	start := time.Now()
	out, err := ResticRun(ctx, st, pw, append([]string{"backup", "--tag", "laib", "--exclude-caches"}, paths...)...)
	for _, name := range quiesced {
		for _, u := range st.Services[name].UnitNames() {
			sysd.Run(ctx, "systemctl", "--user", "start", u)
		}
		apply.Resume(env.Paths, name)
	}
	entry := journal.Entry{Actor: env.Actor, Verb: "backup", MS: time.Since(start).Milliseconds(), Detail: cli.Tail(out, 400)}
	if len(quiesced) > 0 {
		entry.Detail = "angehalten für den Snapshot: " + strings.Join(quiesced, ", ") + "\n" + entry.Detail
	}
	if err != nil {
		entry.Result = "red"
		journal.Append(env.Paths.Journal, entry)
		return env.Fail(err)
	}
	entry.Result = "green"
	journal.Append(env.Paths.Journal, entry)
	if _, err := ResticRun(ctx, st, pw, "forget", "--tag", "laib", "--keep-daily", "14", "--keep-weekly", "8", "--keep-monthly", "12", "--prune"); err != nil {
		journal.Append(env.Paths.Journal, journal.Entry{Actor: env.Actor, Verb: "backup", Result: "red", Detail: "forget: " + err.Error()})
	}
	if env.JSON {
		return cli.Emit(env, map[string]any{"paths": paths, "ms": entry.MS})
	}
	if len(quiesced) > 0 {
		fmt.Fprintf(env.W(), "angehalten für den Snapshot: %s (wieder gestartet)\n", strings.Join(quiesced, ", "))
	}
	fmt.Fprintf(env.W(), "grün · %d Pfade · %s\n", len(paths), cli.Tail(out, 200))
	return cli.ExitOK
}

// backupCheck is the monthly restore test (#40): restic verifies a sample of
// the repo's data, and one service's first backed-up path is restored into
// a scratch directory and compared with what is on disk. The result is a
// journal line (verb backup, reason check) that diag and the box page read.
func backupCheck(ctx context.Context, env *cli.Env, st *state.State, pw string) int {
	start := time.Now()
	entry := journal.Entry{Actor: env.Actor, Verb: "backup", Reason: "check"}
	fail1 := func(what string, err error) int {
		entry.Result, entry.Detail, entry.MS = "red", what+": "+err.Error(), time.Since(start).Milliseconds()
		journal.Append(env.Paths.Journal, entry)
		return env.Fail(fmt.Errorf("%s: %w", what, err))
	}
	if out, err := ResticRun(ctx, st, pw, "check", "--read-data-subset=5%"); err != nil {
		return fail1("restic check", fmt.Errorf("%s", cli.Tail(out, 300)))
	}
	// one service with a backed-up path: the first in dependency order
	var probe string
	for _, name := range st.Order(st.ServiceNames()) {
		if ps := state.SnapshotPaths(st, st.Services[name]); len(ps) > 0 && ps[0] != "" {
			probe = ps[0]
			break
		}
	}
	if probe == "" {
		probe = env.Paths.Journal
	}
	scratch, err := os.MkdirTemp(env.Paths.Home, ".restore-test-*")
	if err != nil {
		return fail1("scratch", err)
	}
	defer os.RemoveAll(scratch)
	if out, err := ResticRun(ctx, st, pw, "restore", "latest:"+probe, "--target", scratch); err != nil {
		return fail1("restore "+probe, fmt.Errorf("%s", cli.Tail(out, 300)))
	}
	if out, err := sysd.Run(ctx, "diff", "-rq", scratch, probe); err != nil && !strings.Contains(out, "Only in "+probe) {
		// files only on disk are newer than the snapshot; a differing file is not
		return fail1("compare "+probe, fmt.Errorf("%s", cli.Tail(out, 300)))
	}
	entry.Result, entry.Detail, entry.MS = "green", "restic check 5% ok; "+probe+" restored into a scratch dir and compared", time.Since(start).Milliseconds()
	journal.Append(env.Paths.Journal, entry)
	fmt.Fprintln(env.W(), "grün ·", entry.Detail)
	return cli.ExitOK
}

// Restore: `laib restore <snapshot|latest> [service]` restores one
// service's declared paths (replacing what is there) and applies it;
// `laib restore --box <snapshot|latest>` is disaster recovery on a fresh
// box (#40): the state repo and the journal first, then every service in
// dependency order with its units stopped and its paths replaced, then one
// apply. The password comes from secrets.age or, on a fresh box, from the
// recovery sheet (RESTIC_PASSWORD). Everything under the one lock.
func Restore(ctx context.Context, env *cli.Env, args []string) int {
	box := false
	var rest []string
	for _, a := range args {
		if a == "--box" {
			box = true
		} else {
			rest = append(rest, a)
		}
	}
	args = rest
	if len(args) == 0 || (!box && len(args) < 2) {
		return env.Fail(errors.New("usage: laib restore <snapshot|latest> <service>  |  laib restore --box <snapshot|latest>"))
	}
	st, pw, err := Prep(env)
	if err != nil {
		return env.Fail(err)
	}
	if !policy.Allowed(st.Policy, proposal.Hand(env.Actor), "apply") {
		return cli.Refused(env, env.Actor+" may not restore")
	}
	if box && proposal.Hand(env.Actor) != "human" {
		return cli.Refused(env, "restore --box is the human's step")
	}
	snapshot, only := args[0], ""
	if len(args) > 1 {
		only = args[1]
	}
	var paths []string
	if only == "" {
		paths = []string{env.Paths.State, env.Paths.Journal}
	} else {
		svc, ok := st.Services[only]
		if !ok {
			return env.Fail(fmt.Errorf("unknown service %q", only))
		}
		paths = state.SnapshotPaths(st, svc)
		if len(paths) == 0 {
			return env.Fail(fmt.Errorf("%s declares no data paths to restore", only))
		}
	}
	// "latest" means the latest regular backup (tag laib), resolved before the
	// pre-restore snapshot below exists — otherwise latest would be that one,
	// the state just before the restore, and nothing would change (VM run 25).
	if snapshot == "latest" {
		out, err := ResticRun(ctx, st, pw, "snapshots", "--json", "--tag", "laib", "--latest", "1")
		if err != nil {
			return env.Fail(err)
		}
		var snaps []struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(out[max(strings.IndexAny(out, "["), 0):]), &snaps); err != nil || len(snaps) == 0 {
			return env.Fail(fmt.Errorf("no regular backup (tag laib) in the repo yet: %s", cli.Tail(out, 200)))
		}
		snapshot = snaps[0].ID[:8]
		fmt.Fprintf(env.W(), "latest = %s\n", snapshot)
	}
	// the one lock from here to the final verify, and a pause so a converge
	// that outlives a killed restore does not restart on half-written data (#39)
	unlock, code := apply.TakeLock(env, st, "restore")
	if code != cli.ExitOK {
		return code
	}
	defer unlock()
	if only != "" {
		apply.WritePause(env.Paths, apply.Pause{Service: only, By: "restore:" + env.Actor, Until: time.Now().Add(2 * time.Hour).UTC(), Reason: "restore " + snapshot})
		defer apply.Resume(env.Paths, only)
	}
	// What is there now goes into the repo first, so a restore is reversible.
	if _, err := ResticRun(ctx, st, pw, append([]string{"backup", "--tag", "pre-restore"}, paths...)...); err != nil {
		return env.Fail(err)
	}
	for _, unit := range unitsOf(st, only) {
		sysd.Stop(ctx, unit)
	}
	// One restore per path, straight into the path (restic's snapshot:subfolder
	// form): a restore with --target / would also try to chown the parents,
	// and on CoreOS /mnt is part of the immutable root (lchown: read-only).
	// --delete: what the snapshot does not have goes (a restore replaces, it
	// does not overlay — half-new pgdata is no database, #40).
	restorePaths := func(ps []string) (string, error) {
		var outs []string
		for _, p := range ps {
			out, err := ResticRun(ctx, st, pw, "restore", snapshot+":"+p, "--target", p, "--delete")
			outs = append(outs, out)
			if err != nil {
				return strings.Join(outs, "\n"), err
			}
		}
		return strings.Join(outs, "\n"), nil
	}
	out, err := restorePaths(paths)
	entry := journal.Entry{Actor: env.Actor, Verb: "restore", Service: only, Reason: snapshot, Detail: cli.Tail(out, 400)}
	if err != nil {
		entry.Result = "red"
		journal.Append(env.Paths.Journal, entry)
		return env.Fail(err)
	}
	entry.Result = "green"
	journal.Append(env.Paths.Journal, entry)
	// Start what was stopped: the apply below restarts only what its plan
	// changes, and a restore changes data, not rendered files (VM run 11 of
	// #70 waited a minute for a unit nobody started). Units in a pod go
	// through the pod unit, as apply does.
	for _, unit := range apply.PodRestartSet(unitsOf(st, only)) {
		if err := sysd.Restart(ctx, unit); err != nil {
			return env.Fail(fmt.Errorf("start %s after restore: %w", unit, err))
		}
	}
	st, err = cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	if box {
		// the restored state is the truth now: every service, roles first,
		// stopped while its paths are replaced; then one apply brings it up
		if sec, err := secrets.Load(env.Paths.Secrets, env.Paths.Key); err == nil && sec[ResticPassword] != "" {
			pw = sec[ResticPassword]
		}
		names := st.Order(st.ServiceNames())
		for _, name := range names {
			ps := state.SnapshotPaths(st, st.Services[name])
			if len(ps) == 0 {
				continue
			}
			apply.WritePause(env.Paths, apply.Pause{Service: name, By: "restore:" + env.Actor, Until: time.Now().Add(2 * time.Hour).UTC(), Reason: "restore --box " + snapshot})
			for _, u := range st.Services[name].UnitNames() {
				sysd.Stop(ctx, u)
			}
			out, err := restorePaths(ps)
			e := journal.Entry{Actor: env.Actor, Verb: "restore", Service: name, Reason: snapshot, Result: "green", Detail: cli.Tail(out, 300)}
			if err != nil {
				e.Result, e.Detail = "red", err.Error()
				journal.Append(env.Paths.Journal, e)
				return env.Fail(fmt.Errorf("restore %s: %w", name, err))
			}
			journal.Append(env.Paths.Journal, e)
		}
		defer func() {
			for _, name := range names {
				apply.Resume(env.Paths, name)
			}
		}()
		fmt.Fprintf(env.W(), "Box wiederhergestellt aus %s: State, Journal, %d Dienste; jetzt apply\n", snapshot, len(names))
	}
	return apply.ApplyHeld(ctx, env, st, only, "restore "+snapshot)
}

func unitsOf(st *state.State, only string) []string {
	if only == "" {
		return nil
	}
	if svc, ok := st.Services[only]; ok {
		return svc.UnitNames()
	}
	return nil
}
