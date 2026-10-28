package inbox

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/backup"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

func init() { apply.Carry = Carry }

// Carry is the apply worker (#609): an approve only merges, and whoever
// gets the lock next applies main with every proposal still waiting as one
// run, named by them and its commit range. Each release of the lock starts
// it again, so N approvals during one apply give one more apply. A red run
// is rolled back as a whole: each of its proposals says with which run.
func Carry(ctx context.Context, env *cli.Env) int {
	if len(proposal.Waiting(cli.InboxDir(env))) == 0 {
		return cli.ExitOK
	}
	apply.AwaitLock(env) // a shell approve waits for the run before it (env.Wait), which may carry this one too
	if len(proposal.Waiting(cli.InboxDir(env))) == 0 {
		return cli.ExitOK
	}
	unlock, code := apply.TakeLock(env, nil, "apply")
	if code == cli.ExitLocked {
		fmt.Fprintln(env.W(), "freigegeben: wird mit dem nächsten Lauf angewendet")
		return cli.ExitOK
	} else if code != cli.ExitOK {
		return code
	}
	defer unlock()
	release := proposal.LockRepo(env.Paths.State) // the run and the state it applies, as one
	batch := proposal.Waiting(cli.InboxDir(env))
	st, err := cli.LoadState(env)
	if len(batch) == 0 { // carried meanwhile
		release()
		return cli.ExitOK
	}
	from, _ := sysd.Run(ctx, "git", "-C", env.Paths.State, "rev-parse", "--short", batch[0].Merge+"^1")
	to, _ := sysd.Run(ctx, "git", "-C", env.Paths.State, "rev-parse", "--short", "HEAD")
	release()
	if err != nil {
		return env.Fail(err)
	}
	// each step of the run is on its proposals, for the Inbox (#686)
	because, prefix := "weil die Box beim Einrichten einen Fehler hatte (Näheres im Journal)", ""
	say := func(step string, red bool) {
		if red && prefix == "" {
			because = "weil " + step
		}
		for _, p := range batch {
			p.Step = prefix + step
			proposal.Save(cli.InboxDir(env), p)
		}
	}
	ctx = apply.WithProgress(ctx, say)
	defer func() {
		for _, p := range batch {
			p.Step = ""
			proposal.Save(cli.InboxDir(env), p)
		}
	}()
	run := ""
	for _, p := range batch {
		run += fmt.Sprintf(", #%d", p.ID)
		for _, name := range p.Snapshot { // a service with data (#528): its snapshot before the run touches it, fail closed
			if svc := st.Services[name]; svc != nil && code == cli.ExitOK {
				say("sichert "+name, false)
				if code = backup.Pre(ctx, env, st, svc, "apply", "pre-update", true); code != cli.ExitOK {
					because = "weil die Sicherung von " + name + " nicht ging"
				}
			}
		}
	}
	run = fmt.Sprintf("%s (%s..%s)", run[2:], strings.TrimSpace(from), strings.TrimSpace(to))
	if code == cli.ExitOK {
		code = apply.ApplyHeld(ctx, env, st, "", "Freigaben "+run)
	}
	for _, p := range batch {
		p.Outcome, p.Batch = "", run
		if code != cli.ExitOK {
			p.Outcome, p.Settled, p.Because = "rolled-back", time.Now().UTC(), because+others(batch, p)
		}
		proposal.Save(cli.InboxDir(env), p)
	}
	if code == cli.ExitOK {
		return code
	}
	say("macht die Änderung rückgängig", false)
	prefix = "macht rückgängig: "
	for _, p := range slices.Backward(batch) { // the run's merges, newest first
		if _, err := RevertProposal(ctx, env, st, p.ID, ", mit dem ganzen Lauf "+run); err != nil {
			cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "rollback", Result: "red", Detail: "revert failed: " + err.Error()})
			return cli.ExitRed
		}
	}
	if st, err = cli.LoadState(env); err != nil {
		return env.Fail(err)
	}
	apply.ApplyHeld(ctx, env, st, "", "rollback "+run)
	return code
}

// others names the rest of a red run p was taken back with (#609, #686).
func others(batch []*proposal.Proposal, p *proposal.Proposal) string {
	var ids []string
	for _, o := range batch {
		if o != p {
			ids = append(ids, fmt.Sprintf("#%d", o.ID))
		}
	}
	if len(ids) == 0 {
		return ""
	}
	return " – mit " + strings.Join(ids, ", ") + " im selben Durchgang"
}

// Rollback: `laib rollback [#<proposal> | <commit>]` (#41). By proposal
// it reverts the approve merge (-m 1) without touching secrets.age, drops
// the secrets of services that leave the state, and applies — the plan's
// manifest then stops and removes the units and files. By commit the
// argument is validated first (never an option for git); a merge commit
// is reverted with -m 1.
func Rollback(ctx context.Context, env *cli.Env, args []string) int {
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	if !policy.Allowed(st.Policy, proposal.Hand(env.Actor), "apply") {
		return cli.Refused(env, env.Actor+" may not rollback")
	}
	arg := lastChange(ctx, env.Paths.State)
	if len(args) > 0 {
		arg = args[0]
	}
	if strings.HasPrefix(arg, "-") {
		return env.Fail(fmt.Errorf("%q ist kein Commit", arg))
	}
	apply.AwaitLock(env) // behind the work before it, like an approve (#854)
	unlock, code := apply.TakeLock(env, st, "rollback")
	if code != cli.ExitOK {
		return code
	}
	defer unlock()
	if strings.HasPrefix(arg, "#") {
		id, err := strconv.Atoi(strings.TrimPrefix(arg, "#"))
		if err != nil {
			return env.Fail(fmt.Errorf("%q ist keine Vorschlagsnummer", arg))
		}
		subject, err := RevertProposal(ctx, env, st, id, "")
		if err != nil {
			return env.Fail(err)
		}
		st, err = cli.LoadState(env)
		if err != nil {
			return env.Fail(err)
		}
		return apply.ApplyHeld(ctx, env, st, "", "rollback "+subject)
	}
	sha, err := sysd.Run(ctx, "git", "-C", env.Paths.State, "rev-parse", "--verify", "--quiet", "--end-of-options", arg+"^{commit}")
	if err != nil || strings.TrimSpace(sha) == "" {
		return env.Fail(fmt.Errorf("Commit %q gibt es nicht", arg))
	}
	sha = strings.TrimSpace(sha)
	subject, _ := sysd.Run(ctx, "git", "-C", env.Paths.State, "log", "-1", "--format=%h %s", "--end-of-options", sha)
	subject = strings.TrimSpace(subject)
	revert := []string{"-C", env.Paths.State, "-c", "user.name=laib", "-c", "user.email=laib@localhost", "revert", "--no-edit"}
	if parents, _ := sysd.Run(ctx, "git", "-C", env.Paths.State, "rev-list", "--parents", "-n", "1", sha); len(strings.Fields(parents)) > 2 {
		revert = append(revert, "-m", "1")
	}
	if out, err := sysd.Run(ctx, "git", append(revert, sha)...); err != nil {
		sysd.Run(ctx, "git", "-C", env.Paths.State, "revert", "--abort")
		return env.Fail(fmt.Errorf("revert: %s", out))
	}
	journal.Append(env.Paths.Journal, journal.Entry{Actor: env.Actor, Verb: "rollback", Result: "info", Detail: subject})
	st, err = cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	return apply.ApplyHeld(ctx, env, st, "", "rollback "+subject)
}

// RevertProposal reverts the approve merge of proposal id (#41): the tree
// without the proposal, secrets.age as it is now (the running services'
// secrets stay), minus the secrets of services the revert takes out of
// the state. with names the run it went with. Returns the subject for the journal.
func RevertProposal(ctx context.Context, env *cli.Env, st *state.State, id int, with string) (string, error) {
	defer proposal.LockRepo(env.Paths.State)() // beside an approve's merge (#609)
	g := func(args ...string) (string, error) {
		return sysd.Run(ctx, "git", append([]string{"-C", env.Paths.State, "-c", "user.name=laib", "-c", "user.email=laib@localhost"}, args...)...)
	}
	merge, err := g("log", "--format=%H", "--grep=^approve #"+strconv.Itoa(id)+" by ", "-n", "1", "main")
	if err != nil || strings.TrimSpace(merge) == "" {
		return "", fmt.Errorf("keine Freigabe von #%d in der Geschichte des States: nichts zurückzunehmen", id)
	}
	merge = strings.TrimSpace(merge)
	before := st.ServiceNames()
	if out, err := g("revert", "--no-commit", "-m", "1", merge); err != nil {
		g("revert", "--abort")
		return "", fmt.Errorf("#%d lässt sich nicht zurücknehmen, git revert sagt: %s", id, out)
	}
	// secrets.age stays what it is: the services still there keep the
	// secrets their data was made with
	g("checkout", "HEAD", "--", "secrets.age")
	if out, err := g("commit", "-q", "-m", fmt.Sprintf("rollback #%d", id)); err != nil {
		g("revert", "--abort")
		return "", fmt.Errorf("Rücknahme nicht gespeichert, git commit sagt: %s", out)
	}
	after, err := cli.LoadState(env)
	if err != nil {
		return "", fmt.Errorf("der State ohne #%d lädt nicht: %w", id, err)
	}
	gone := map[string]bool{}
	for _, n := range before {
		if _, ok := after.Services[n]; !ok {
			gone[n] = true
		}
	}
	if len(gone) > 0 {
		sec, err := secrets.Load(env.Paths.Secrets, env.Paths.Key)
		if err == nil {
			for n := range gone {
				for _, sdecl := range st.Services[n].Contract.Secrets {
					delete(sec, sdecl.Name)
				}
			}
			if err := secrets.Save(env.Paths.Secrets, env.Paths.Key, sec); err != nil {
				return "", err
			}
			g("add", "-A")
			g("commit", "-q", "-m", fmt.Sprintf("rollback #%d: secrets of removed services dropped", id))
		}
	}
	if p, err := proposal.Get(cli.InboxDir(env), id); err == nil { // what came of it (#544)
		p.Outcome, p.Settled = "rolled-back", time.Now().UTC()
		proposal.Save(cli.InboxDir(env), p)
	}
	subject := fmt.Sprintf("#%d zurückgenommen%s", id, with)
	journal.Append(env.Paths.Journal, journal.Entry{Actor: env.Actor, Verb: "rollback", Result: "info", Detail: subject + " (" + merge[:8] + ")"})
	return subject, nil
}

// lastChange is what a bare rollback reverts: the newest commit on main
// that changed more than aufgaben.yml (#667). Ticking a Box-Aufgabe off is
// bookkeeping on top of the box's last change, not that change.
func lastChange(ctx context.Context, repo string) string {
	log, _ := sysd.Run(ctx, "git", "-C", repo, "log", "--first-parent", "-n", "50", "--format=%H")
	for _, sha := range strings.Fields(log) {
		files, err := sysd.Run(ctx, "git", "-C", repo, "diff", "--name-only", sha+"^1", sha)
		if err != nil || strings.TrimSpace(files) != "aufgaben.yml" {
			return sha
		}
	}
	return "HEAD"
}
