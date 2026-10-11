package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

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
	if !ok {
		pw = secrets.Generate()
		sec[resticPassword] = pw
		if err := secrets.Save(env.paths.Secrets, env.paths.Key, sec); err != nil {
			return nil, "", err
		}
		fmt.Fprintln(os.Stderr, "laib: RESTIC_PASSWORD generated; `laib secret list` shows it exists, keep a copy with the age key")
	}
	return st, pw, nil
}

func cmdBackup(ctx context.Context, env *cliEnv, args []string) int {
	st, pw, err := backupPrep(env)
	if err != nil {
		return fail(err)
	}
	if !policy.Allowed(st.Policy, proposal.Hand(env.actor), "propose") {
		return exitRefusedMsg(env, env.actor+" may not run a backup")
	}
	if _, err := resticRun(ctx, st, pw, "cat", "config"); err != nil {
		if _, err := resticRun(ctx, st, pw, "init"); err != nil {
			journal.Append(env.paths.Journal, journal.Entry{Actor: env.actor, Verb: "backup", Result: "red", Detail: err.Error()})
			return fail(err)
		}
	}
	paths := render.BackupPaths(st, env.paths.State, env.paths.Journal)
	start := time.Now()
	out, err := resticRun(ctx, st, pw, append([]string{"backup", "--tag", "laib", "--exclude-caches"}, paths...)...)
	entry := journal.Entry{Actor: env.actor, Verb: "backup", MS: time.Since(start).Milliseconds(), Detail: tail(out, 400)}
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
	fmt.Printf("grün · %d Pfade · %s\n", len(paths), tail(out, 200))
	return exitOK
}

// cmdRestore: `laib restore <snapshot|latest> [service]`. Without a service
// it restores the state repo and the journal (a fresh box); with one, that
// service's declared paths. Either way it snapshots what is there first and
// ends with an apply.
func cmdRestore(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) == 0 {
		return fail(errors.New("usage: laib restore <snapshot|latest> [service]"))
	}
	st, pw, err := backupPrep(env)
	if err != nil {
		return fail(err)
	}
	if !policy.Allowed(st.Policy, proposal.Hand(env.actor), "apply") {
		return exitRefusedMsg(env, env.actor+" may not restore")
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
		fmt.Printf("latest = %s\n", snapshot)
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
	var outs []string
	err = nil
	for _, p := range paths {
		var out string
		out, err = resticRun(ctx, st, pw, "restore", snapshot+":"+p, "--target", p)
		outs = append(outs, out)
		if err != nil {
			break
		}
	}
	out := strings.Join(outs, "\n")
	entry := journal.Entry{Actor: env.actor, Verb: "restore", Service: only, Reason: snapshot, Detail: tail(out, 400)}
	if err != nil {
		entry.Result = "red"
		journal.Append(env.paths.Journal, entry)
		return fail(err)
	}
	entry.Result = "green"
	journal.Append(env.paths.Journal, entry)
	st, err = loadState(env)
	if err != nil {
		return fail(err)
	}
	return runApply(ctx, env, st, only, "restore "+snapshot)
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
