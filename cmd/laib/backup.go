package main

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
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// resticPassword is the secret name; generated on first backup, print it and
// keep it with the age key.
const resticPassword = "RESTIC_PASSWORD"

func restic(ctx context.Context, st *state.State, pw string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "restic", args...)
	cmd.Env = append(os.Environ(), "RESTIC_REPOSITORY="+st.Box.Backup.Repo, "RESTIC_PASSWORD="+pw)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return text, fmt.Errorf("restic %s: %s", args[0], text)
	}
	return text, nil
}

// resticRun is the seam tests swap; it defaults to the real binary.
var resticRun = restic

func backupPrep(env *cliEnv) (*state.State, string, error) {
	st, err := loadState(env)
	if err != nil {
		return nil, "", err
	}
	if st.Box.Backup.Repo == "" {
		return nil, "", errors.New("box.yml has no backup.repo")
	}
	sec, err := secrets.Load(env.paths.Secrets, env.paths.Key)
	if err != nil {
		return nil, "", err
	}
	pw, ok := sec[resticPassword]
	if fromSheet := os.Getenv("RESTIC_PASSWORD"); fromSheet != "" {
		// the password from the recovery sheet (a fresh box, #40): it is the
		// truth, and it goes into secrets.age so the next run has it
		pw, ok = fromSheet, true
		if sec[resticPassword] != fromSheet {
			sec[resticPassword] = fromSheet
			if err := secrets.Save(env.paths.Secrets, env.paths.Key, sec); err != nil {
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
		sec[resticPassword] = pw
		if err := secrets.Save(env.paths.Secrets, env.paths.Key, sec); err != nil {
			return nil, "", err
		}
		fmt.Fprintln(os.Stderr, "laib: RESTIC_PASSWORD generated — `laib recovery-sheet` prints it with the age key; print that page")
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
	out, err := resticRun(ctx, st, "x", "cat", "config")
	if err == nil {
		return true
	}
	return !strings.Contains(strings.ToLower(out+" "+err.Error()), "does not exist")
}

func cmdBackup(ctx context.Context, env *cliEnv, args []string) int {
	st, pw, err := backupPrep(env)
	if err != nil {
		return fail(err)
	}
	if !policy.Allowed(st.Policy, proposal.Hand(env.actor), "propose") {
		return exitRefusedMsg(env, env.actor+" may not run a backup")
	}
	if len(args) > 0 && args[0] == "check" {
		return backupCheck(ctx, env, st, pw)
	}
	if _, err := resticRun(ctx, st, pw, "cat", "config"); err != nil {
		if _, err := resticRun(ctx, st, pw, "init"); err != nil {
			journal.Append(env.paths.Journal, journal.Entry{Actor: env.actor, Verb: "backup", Result: "red", Detail: err.Error()})
			return fail(err)
		}
	}
	paths := render.BackupPaths(st, env.paths.State, env.paths.Journal)
	// quiesce: true paths (databases, #40) are snapshotted with their service
	// stopped — under the lock and paused, so converge keeps its hands off
	unlock, code := takeLock(env, st, "backup")
	if code != exitOK {
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
		apply.WritePause(env.paths, apply.Pause{Service: name, By: "backup:" + env.actor, Until: time.Now().Add(time.Hour).UTC(), Reason: "quiesce"})
		for _, u := range st.Services[name].UnitNames() {
			sysd.Stop(ctx, u)
		}
	}
	start := time.Now()
	out, err := resticRun(ctx, st, pw, append([]string{"backup", "--tag", "laib", "--exclude-caches"}, paths...)...)
	for _, name := range quiesced {
		for _, u := range st.Services[name].UnitNames() {
			sysd.Run(ctx, "systemctl", "--user", "start", u)
		}
		apply.Resume(env.paths, name)
	}
	entry := journal.Entry{Actor: env.actor, Verb: "backup", MS: time.Since(start).Milliseconds(), Detail: tail(out, 400)}
	if len(quiesced) > 0 {
		entry.Detail = "angehalten für den Snapshot: " + strings.Join(quiesced, ", ") + "\n" + entry.Detail
	}
	if err != nil {
		entry.Result = "red"
		journal.Append(env.paths.Journal, entry)
		return fail(err)
	}
	entry.Result = "green"
	journal.Append(env.paths.Journal, entry)
	if _, err := resticRun(ctx, st, pw, "forget", "--tag", "laib", "--keep-daily", "14", "--keep-weekly", "8", "--keep-monthly", "12", "--prune"); err != nil {
		journal.Append(env.paths.Journal, journal.Entry{Actor: env.actor, Verb: "backup", Result: "red", Detail: "forget: " + err.Error()})
	}
	if env.json {
		return emit(env, map[string]any{"paths": paths, "ms": entry.MS})
	}
	fmt.Fprintf(env.w(), "grün · %d Pfade · %s\n", len(paths), tail(out, 200))
	return exitOK
}

// backupCheck is the monthly restore test (#40): restic verifies a sample of
// the repo's data, and one service's first backed-up path is restored into
// a scratch directory and compared with what is on disk. The result is a
// journal line (verb backup, reason check) that diag and the box page read.
func backupCheck(ctx context.Context, env *cliEnv, st *state.State, pw string) int {
	start := time.Now()
	entry := journal.Entry{Actor: env.actor, Verb: "backup", Reason: "check"}
	fail1 := func(what string, err error) int {
		entry.Result, entry.Detail, entry.MS = "red", what+": "+err.Error(), time.Since(start).Milliseconds()
		journal.Append(env.paths.Journal, entry)
		return fail(fmt.Errorf("%s: %w", what, err))
	}
	if out, err := resticRun(ctx, st, pw, "check", "--read-data-subset=5%"); err != nil {
		return fail1("restic check", fmt.Errorf("%s", tail(out, 300)))
	}
	// one service with a backed-up path: the first in dependency order
	var probe string
	for _, name := range st.Order(st.ServiceNames()) {
		for _, d := range st.Services[name].Contract.Data {
			if d.Backup == nil || *d.Backup {
				probe = strings.ReplaceAll(d.Path, "${DATA_DIR}", st.Box.DataDir)
				break
			}
		}
		if probe != "" {
			break
		}
	}
	if probe == "" {
		probe = env.paths.Journal
	}
	scratch, err := os.MkdirTemp(env.paths.Home, ".restore-test-*")
	if err != nil {
		return fail1("scratch", err)
	}
	defer os.RemoveAll(scratch)
	if out, err := resticRun(ctx, st, pw, "restore", "latest:"+probe, "--target", scratch); err != nil {
		return fail1("restore "+probe, fmt.Errorf("%s", tail(out, 300)))
	}
	if out, err := sysd.Run(ctx, "diff", "-rq", scratch, probe); err != nil && !strings.Contains(out, "Only in "+probe) {
		// files only on disk are newer than the snapshot; a differing file is not
		return fail1("compare "+probe, fmt.Errorf("%s", tail(out, 300)))
	}
	entry.Result, entry.Detail, entry.MS = "green", "restic check 5% ok; "+probe+" restored into a scratch dir and compared", time.Since(start).Milliseconds()
	journal.Append(env.paths.Journal, entry)
	fmt.Fprintln(env.w(), "grün ·", entry.Detail)
	return exitOK
}

// cmdRestore: `laib restore <snapshot|latest> [service]` restores one
// service's declared paths (replacing what is there) and applies it;
// `laib restore --box <snapshot|latest>` is disaster recovery on a fresh
// box (#40): the state repo and the journal first, then every service in
// dependency order with its units stopped and its paths replaced, then one
// apply. The password comes from secrets.age or, on a fresh box, from the
// recovery sheet (RESTIC_PASSWORD). Everything under the one lock.
func cmdRestore(ctx context.Context, env *cliEnv, args []string) int {
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
		return fail(errors.New("usage: laib restore <snapshot|latest> <service>  |  laib restore --box <snapshot|latest>"))
	}
	st, pw, err := backupPrep(env)
	if err != nil {
		return fail(err)
	}
	if !policy.Allowed(st.Policy, proposal.Hand(env.actor), "apply") {
		return exitRefusedMsg(env, env.actor+" may not restore")
	}
	if box && proposal.Hand(env.actor) != "human" {
		return exitRefusedMsg(env, "restore --box is the human's step")
	}
	snapshot, only := args[0], ""
	if len(args) > 1 {
		only = args[1]
	}
	var paths []string
	if only == "" {
		paths = []string{env.paths.State, env.paths.Journal}
	} else {
		svc, ok := st.Services[only]
		if !ok {
			return fail(fmt.Errorf("unknown service %q", only))
		}
		for _, d := range svc.Contract.Data {
			if d.Backup == nil || *d.Backup {
				paths = append(paths, strings.ReplaceAll(d.Path, "${DATA_DIR}", st.Box.DataDir))
			}
		}
		if len(paths) == 0 {
			return fail(fmt.Errorf("%s declares no data paths to restore", only))
		}
	}
	// "latest" means the latest regular backup (tag laib), resolved before the
	// pre-restore snapshot below exists — otherwise latest would be that one,
	// the state just before the restore, and nothing would change (VM run 25).
	if snapshot == "latest" {
		out, err := resticRun(ctx, st, pw, "snapshots", "--json", "--tag", "laib", "--latest", "1")
		if err != nil {
			return fail(err)
		}
		var snaps []struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(out[max(strings.IndexAny(out, "["), 0):]), &snaps); err != nil || len(snaps) == 0 {
			return fail(fmt.Errorf("no regular backup (tag laib) in the repo yet: %s", tail(out, 200)))
		}
		snapshot = snaps[0].ID[:8]
		fmt.Fprintf(env.w(), "latest = %s\n", snapshot)
	}
	// the one lock from here to the final verify, and a pause so a converge
	// that outlives a killed restore does not restart on half-written data (#39)
	unlock, code := takeLock(env, st, "restore")
	if code != exitOK {
		return code
	}
	defer unlock()
	if only != "" {
		apply.WritePause(env.paths, apply.Pause{Service: only, By: "restore:" + env.actor, Until: time.Now().Add(2 * time.Hour).UTC(), Reason: "restore " + snapshot})
		defer apply.Resume(env.paths, only)
	}
	// What is there now goes into the repo first, so a restore is reversible.
	if _, err := resticRun(ctx, st, pw, append([]string{"backup", "--tag", "pre-restore"}, paths...)...); err != nil {
		return fail(err)
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
			out, err := resticRun(ctx, st, pw, "restore", snapshot+":"+p, "--target", p, "--delete")
			outs = append(outs, out)
			if err != nil {
				return strings.Join(outs, "\n"), err
			}
		}
		return strings.Join(outs, "\n"), nil
	}
	out, err := restorePaths(paths)
	entry := journal.Entry{Actor: env.actor, Verb: "restore", Service: only, Reason: snapshot, Detail: tail(out, 400)}
	if err != nil {
		entry.Result = "red"
		journal.Append(env.paths.Journal, entry)
		return fail(err)
	}
	entry.Result = "green"
	journal.Append(env.paths.Journal, entry)
	// Start what was stopped: the apply below restarts only what its plan
	// changes, and a restore changes data, not rendered files (VM run 11 of
	// #70 waited a minute for a unit nobody started). Units in a pod go
	// through the pod unit, as apply does.
	for _, unit := range apply.PodRestartSet(unitsOf(st, only)) {
		if err := sysd.Restart(ctx, unit); err != nil {
			return fail(fmt.Errorf("start %s after restore: %w", unit, err))
		}
	}
	st, err = loadState(env)
	if err != nil {
		return fail(err)
	}
	if box {
		// the restored state is the truth now: every service, roles first,
		// stopped while its paths are replaced; then one apply brings it up
		if sec, err := secrets.Load(env.paths.Secrets, env.paths.Key); err == nil && sec[resticPassword] != "" {
			pw = sec[resticPassword]
		}
		names := st.Order(st.ServiceNames())
		for _, name := range names {
			var ps []string
			for _, d := range st.Services[name].Contract.Data {
				if d.Backup == nil || *d.Backup {
					ps = append(ps, strings.ReplaceAll(d.Path, "${DATA_DIR}", st.Box.DataDir))
				}
			}
			if len(ps) == 0 {
				continue
			}
			apply.WritePause(env.paths, apply.Pause{Service: name, By: "restore:" + env.actor, Until: time.Now().Add(2 * time.Hour).UTC(), Reason: "restore --box " + snapshot})
			for _, u := range st.Services[name].UnitNames() {
				sysd.Stop(ctx, u)
			}
			out, err := restorePaths(ps)
			e := journal.Entry{Actor: env.actor, Verb: "restore", Service: name, Reason: snapshot, Result: "green", Detail: tail(out, 300)}
			if err != nil {
				e.Result, e.Detail = "red", err.Error()
				journal.Append(env.paths.Journal, e)
				return fail(fmt.Errorf("restore %s: %w", name, err))
			}
			journal.Append(env.paths.Journal, e)
		}
		defer func() {
			for _, name := range names {
				apply.Resume(env.paths, name)
			}
		}()
		fmt.Fprintf(env.w(), "Box wiederhergestellt aus %s: State, Journal, %d Dienste; jetzt apply\n", snapshot, len(names))
	}
	return applyHeld(ctx, env, st, only, "restore "+snapshot)
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

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
