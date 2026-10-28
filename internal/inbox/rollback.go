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
	// a shell approve waits for the run before it (env.Wait), which may carry this one too
	for until := time.Now().Add(env.Wait); apply.LockHolder(env.Paths) != "" && time.Now().Before(until); {
		time.Sleep(time.Second)
	}
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
	run, snaps, ran := "", []snap(nil), false
	for _, p := range batch {
		run += fmt.Sprintf(", #%d", p.ID)
		for _, name := range p.Snapshot { // a service with data (#528): its snapshot before the run touches it, fail closed
			if svc := st.Services[name]; svc != nil && code == cli.ExitOK {
				say("sichert "+name, false)
				id, at := "", time.Now() // restic's time of the snapshot is its start
				if id, code = backup.Pre(ctx, env, st, svc, "apply", "pre-update", true); code != cli.ExitOK {
					because = "weil die Sicherung von " + name + " nicht ging"
				} else if id != "" {
					snaps = append(snaps, snap{name, id, at})
				}
			}
		}
	}
	run = fmt.Sprintf("%s (%s..%s)", run[2:], strings.TrimSpace(from), strings.TrimSpace(to))
	if code == cli.ExitOK {
		code, ran = apply.ApplyHeld(ctx, env, st, "", "Freigaben "+run), true
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
	back := apply.ApplyHeld(ctx, env, st, "", "rollback "+run) == cli.ExitOK
	if ran && len(snaps) > 0 { // the run touched services with data: their snapshots are there, nothing restores by itself (#853)
		offer(ctx, env, batch, snaps, back)
	}
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
		return env.Fail(fmt.Errorf("%q is not a commit", arg))
	}
	unlock, code := apply.TakeLock(env, st, "rollback")
	if code != cli.ExitOK {
		return code
	}
	defer unlock()
	if strings.HasPrefix(arg, "#") {
		id, err := strconv.Atoi(strings.TrimPrefix(arg, "#"))
		if err != nil {
			return env.Fail(fmt.Errorf("%q is not a proposal", arg))
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
		return env.Fail(fmt.Errorf("unknown commit %q", arg))
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
		return "", fmt.Errorf("no approve of #%d in the state's history", id)
	}
	merge = strings.TrimSpace(merge)
	before := st.ServiceNames()
	if out, err := g("revert", "--no-commit", "-m", "1", merge); err != nil {
		g("revert", "--abort")
		return "", fmt.Errorf("revert of #%d: %s", id, out)
	}
	// secrets.age stays what it is: the services still there keep the
	// secrets their data was made with
	g("checkout", "HEAD", "--", "secrets.age")
	if out, err := g("commit", "-q", "-m", fmt.Sprintf("rollback #%d", id)); err != nil {
		g("revert", "--abort")
		return "", fmt.Errorf("rollback commit: %s", out)
	}
	after, err := cli.LoadState(env)
	if err != nil {
		return "", fmt.Errorf("the state without #%d does not load: %w", id, err)
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

// snap is a service's snapshot a run took before it touched it (#853).
type snap struct {
	svc, id string
	at      time.Time
}

// offer is a red run's two ways on for its services with data (#853,
// owner 2026-10-28; ADR 0019 §4 as amended): the old program is back, the
// data are as the update left them, and nothing restores by itself. Its
// cards say so with the snapshots' times, and laib files the two
// proposals they offer: „Sicherung zurückspielen“ (the snapshots back,
// laib restore) and „Nach vorne reparieren“ (the run's changes to those
// services on again with the data as they are, laib rollback of the
// revert; a red verify is the Operator's incident, #792).
func offer(ctx context.Context, env *cli.Env, batch []*proposal.Proposal, snaps []snap, back bool) {
	st, err := cli.LoadState(env)
	if err != nil {
		return
	}
	var names, ats, restores, forwards []string
	for _, s := range snaps {
		names = append(names, proposal.Named(s.svc))
		ats = append(ats, s.at.Format("15:04"))
		restores = append(restores, "laib restore "+s.id+" "+s.svc)
	}
	who, at, kept := strings.Join(names, ", "), "der Sicherung von "+ats[0], "Die Sicherung von "+ats[0]+" bleibt eine Woche lang bereit."
	ready := "die Sicherung von " + ats[0] + " liegt bereit"
	if len(snaps) > 1 {
		var each []string
		for i := range names {
			each = append(each, names[i]+" ("+ats[i]+")")
		}
		at, kept = "den Sicherungen von "+strings.Join(ats, ", "), "Die Sicherungen bleiben eine Woche lang bereit."
		ready = "die Sicherungen von " + strings.Join(each, ", ") + " liegen bereit"
	}
	old := "Das alte Programm läuft wieder."
	if !back {
		old = "Das alte Programm ist wieder aufgespielt, läuft aber noch nicht richtig."
	}
	told := old + " Die Daten sind auf dem Stand nach dem Update; " + ready + "."
	for _, p := range batch {
		if len(p.Snapshot) == 0 {
			continue
		}
		sha, _ := sysd.Run(ctx, "git", "-C", env.Paths.State, "log", "--format=%H", "--grep=^rollback #"+strconv.Itoa(p.ID)+"$", "-n", "1", "main")
		if sha = strings.TrimSpace(sha); sha != "" {
			forwards = append(forwards, "laib rollback "+sha)
		}
	}
	r, rerr := fileRepair(ctx, env, st, who+": Daten aus "+at+" zurückspielen", "Das Update von "+who+" lief nicht. "+told, "Die Daten von jetzt sichert Laib vorher; aus dieser Sicherung kommen sie zurück, nicht mit einem Tipp.", nil, restores)
	var f *proposal.Proposal
	if len(forwards) > 0 {
		f, _ = fileRepair(ctx, env, st, who+": nach vorne reparieren, die neue Version wieder aufspielen", "Das Update von "+who+" lief nicht. Statt die Sicherung zurückzuspielen, kommt die neue Version wieder auf die Daten nach dem Update.",
			"Zurück zum alten Stand geht es danach mit „Sicherung zurückspielen“.", []string{onward(st, false), kept}, forwards)
	}
	for _, p := range batch {
		p.Because = strings.TrimSuffix(p.Because, ".") + ". " + told
		if rerr == nil {
			p.Restore = r.ID
		}
		if f != nil {
			p.Forward = f.ID
		}
		proposal.Save(cli.InboxDir(env), p)
	}
	detail := told + " Nichts wird von selbst zurückgespielt."
	if rerr != nil {
		detail += " Der Vorschlag, die Sicherung zurückzuspielen, ging nicht: " + rerr.Error()
	}
	cli.Record(env, st, journal.Entry{Actor: "laib", Verb: "rollback", Result: "info", Detail: detail})
}

// onward is what follows „Nach vorne reparieren“ when the new version
// still does not run (#853): the Operator's incident, bounded as #792.
// red: it is already red, said as a fact rather than as a condition.
func onward(st *state.State, red bool) string {
	switch {
	case st.Box.Operator.Enabled && red:
		return "Der Operator arbeitet das als Vorfall ab; kommt er 30 Minuten lang nicht dazu, holt er Hilfe."
	case st.Box.Operator.Enabled:
		return "Läuft sie danach nicht, arbeitet der Operator das als Vorfall ab; kommt er 30 Minuten lang nicht dazu, holt er Hilfe."
	case red:
		return "Der Operator ist ausgeschaltet: es bleibt so, bis jemand nachsieht."
	}
	return "Läuft sie danach nicht, bleibt sie so, bis jemand nachsieht: der Operator ist ausgeschaltet."
}
