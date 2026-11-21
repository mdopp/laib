package inbox

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/aufgaben"
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
// of several is taken back as a whole and then applied again one proposal
// at a time, in order (#1209): a green one stays, a red one and one that
// builds on it go back alone, and each card says which went through.
func Carry(ctx context.Context, env *cli.Env) int {
	Release(ctx, env) // a held approval whose wait is over (#1194)
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
	l := &lauf{env: env, batch: batch, cur: batch, because: unknownRed}
	ctx = apply.WithWait(apply.WithProgress(ctx, l.say), func(task string) { l.wait = task })
	defer func() {
		for _, p := range batch {
			p.Step = ""
			proposal.Save(cli.InboxDir(env), p)
		}
	}()
	run, snaps, ran := "", []snap(nil), false
	code, l.because = checked(env, st, batch, code, l.because) // what it is about to run, checked by the kernel itself before anything (ADR 0035)
	for _, p := range batch {
		run += fmt.Sprintf(", #%d", p.ID)
		for _, name := range p.Snapshot { // a service with data (#528): its snapshot before the run touches it, fail closed
			if svc := st.Services[name]; svc != nil && code == cli.ExitOK {
				l.say("sichert "+name, false)
				id, at := "", time.Now() // restic's time of the snapshot is its start
				if id, code = backup.Pre(ctx, env, st, svc, "apply", "pre-update", true); code != cli.ExitOK {
					l.because = "weil die Sicherung von " + name + " nicht ging"
				} else if id != "" {
					snaps = append(snaps, snap{name, id, at})
				}
			}
		}
	}
	l.run = fmt.Sprintf("%s (%s..%s)", run[2:], strings.TrimSpace(from), strings.TrimSpace(to))
	if code == cli.ExitOK {
		code, ran = apply.ApplyHeld(ctx, env, st, "", "Freigaben "+l.run), true
	}
	for _, p := range batch {
		p.Outcome, p.Batch = "", l.run
		if code != cli.ExitOK {
			p.Outcome, p.Settled, p.Because = "rolled-back", time.Now().UTC(), l.because
		}
		proposal.Save(cli.InboxDir(env), p)
	}
	if code == cli.ExitOK {
		return code
	}
	return l.takeBack(ctx, st, snaps, ran)
}

const unknownRed = "weil die Box beim Einrichten einen Fehler hatte (Näheres im Journal)"

// lauf is one red run on its way back (#609, #1209): the proposals whose
// cards show its steps now (cur), why it went red in the owner's words,
// and the task a hook named for a precondition.
type lauf struct {
	env                        *cli.Env
	batch, cur                 []*proposal.Proposal
	run, because, prefix, wait string
}

// say puts a step of the run on its cards, for the Inbox (#686).
func (l *lauf) say(step string, red bool) {
	if red && l.prefix == "" {
		l.because = "weil " + step
	}
	for _, p := range l.cur {
		p.Step = l.prefix + step
		proposal.Save(cli.InboxDir(l.env), p)
	}
}

func (l *lauf) undo() {
	l.say("macht die Änderung rückgängig", false)
	l.prefix = "macht rückgängig: "
}

// takeBack reverts the red run's merges, newest first; a run of several
// that ran (a snapshot that failed: nothing runs without it) is applied
// again one proposal at a time. Then the precondition's cards wait, and
// the red ones with data get their two ways on (#853).
func (l *lauf) takeBack(ctx context.Context, st *state.State, snaps []snap, ran bool) int {
	env, whole, alone := l.env, st, len(l.batch) > 1 && ran
	l.undo()
	for _, p := range slices.Backward(l.batch) {
		if _, err := RevertProposal(ctx, env, st, p.ID, ", mit dem Lauf "+l.run, !alone); err != nil {
			cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "rollback", Result: "red", Detail: "revert failed: " + err.Error()})
			return cli.ExitRed
		}
	}
	red, waits, back := l.batch, map[int]string{l.batch[0].ID: l.wait}, false
	if alone {
		var code int
		if red, waits, back, code = l.alone(ctx); code != cli.ExitOK {
			return code
		}
		dropGone(ctx, env, whole, "rollback "+l.run)
	} else if st, err := cli.LoadState(env); err != nil {
		return env.Fail(err)
	} else {
		back = apply.ApplyHeld(ctx, env, st, "", "rollback "+l.run) == cli.ExitOK
	}
	code, data, kept := cli.ExitOK, []*proposal.Proposal(nil), map[string]bool{}
	for _, p := range red {
		if waits[p.ID] != "" && waitFor(ctx, env, p, waits[p.ID]) {
			continue // a precondition: the card waits for the owner's task, nothing of it ran
		}
		code, data = cli.ExitRed, append(data, p)
		for _, s := range p.Snapshot {
			kept[s] = true
		}
	}
	if alone {
		settle(env, st, l.batch, red, l.run)
	}
	if snaps = slices.DeleteFunc(snaps, func(s snap) bool { return !kept[s.svc] }); ran && len(snaps) > 0 {
		offer(ctx, env, data, snaps, back) // their snapshots are there, nothing restores by itself (#853)
	}
	return code
}

// alone applies each proposal of the reverted run by itself, in its order
// (#1209): its rollback reverted and applied; green stays, red is reverted
// again with its own apply, and one whose change does not go on without a
// red one stays out with it. back: every red one's old state runs again.
func (l *lauf) alone(ctx context.Context) (red []*proposal.Proposal, waits map[int]string, back bool, code int) {
	env, waits, back := l.env, map[int]string{}, true
	for _, p := range l.batch {
		l.cur, l.because, l.prefix, l.wait = []*proposal.Proposal{p}, unknownRed, "", ""
		if err := again(ctx, env, p.ID); err != nil {
			p.Because, red = "weil es auf einer Änderung aufbaut, die nicht durchlief", append(red, p)
			continue
		}
		st, err := cli.LoadState(env)
		if err != nil {
			return nil, nil, false, env.Fail(err)
		}
		if apply.ApplyHeld(ctx, env, st, "", fmt.Sprintf("Freigabe #%d allein, aus dem Lauf %s", p.ID, l.run)) == cli.ExitOK {
			p.Outcome, p.Because, p.Settled = "", "", time.Time{}
			proposal.Save(cli.InboxDir(env), p)
			continue
		}
		p.Because, waits[p.ID], red = l.because, l.wait, append(red, p)
		l.undo()
		if _, err := RevertProposal(ctx, env, st, p.ID, ", allein angewendet", false); err != nil {
			cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "rollback", Result: "red", Detail: "revert failed: " + err.Error()})
			return nil, nil, false, cli.ExitRed
		}
		st, _ = cli.LoadState(env)
		back = apply.ApplyHeld(ctx, env, st, "", fmt.Sprintf("rollback #%d", p.ID)) == cli.ExitOK && back
	}
	return red, waits, back, cli.ExitOK
}

// settle says on each red card of a run of several which of the others
// went through (#1209), and the journal says the run's end in one line.
func settle(env *cli.Env, st *state.State, batch, red []*proposal.Proposal, run string) {
	var ok, bad []string
	for _, p := range batch {
		if slices.Contains(red, p) {
			bad = append(bad, fmt.Sprintf("#%d", p.ID))
		} else {
			ok = append(ok, fmt.Sprintf("#%d", p.ID))
		}
	}
	for _, p := range red {
		if len(ok) == 1 {
			p.Because += " – " + ok[0] + " aus demselben Lauf ist angewendet"
		} else if len(ok) > 1 {
			p.Because += " – " + strings.Join(ok[:len(ok)-1], ", ") + " und " + ok[len(ok)-1] + " aus demselben Lauf sind angewendet"
		}
		proposal.Save(cli.InboxDir(env), p)
	}
	detail := "Lauf " + run + " war rot; jede Freigabe allein angewendet:"
	if len(ok) > 0 {
		detail += " angewendet " + strings.Join(ok, ", ")
	}
	if len(bad) > 0 {
		detail += " zurückgenommen " + strings.Join(bad, ", ")
	}
	cli.Record(env, st, journal.Entry{Actor: "laib", Verb: "rollback", Result: "info", Detail: detail})
}

// waitFor holds p for a precondition a hook named (#1209, `laib-wartet:
// <Titel> | <warum>`): a Box-Aufgabe for the admins, and p's change as a
// fresh card, approved in advance by whoever approved p, that Laib applies
// once the task is done (#1194). False when p is no set of values.
func waitFor(ctx context.Context, env *cli.Env, p *proposal.Proposal, task string) bool {
	st, err := cli.LoadState(env)
	if err != nil || p.Intent == nil || len(p.Intent.Set) == 0 || len(p.Intent.At)+len(p.Intent.Grant) > 0 || p.Intent.Update != "" {
		return false
	}
	titel, warum, _ := strings.Cut(task, " | ")
	var a aufgaben.Aufgabe
	if aufgaben.Update(ctx, env.Paths.State, "laib", func(list []aufgaben.Aufgabe) ([]aufgaben.Aufgabe, string, error) {
		list, a = aufgaben.Add(list, aufgaben.Aufgabe{Titel: strings.TrimSpace(titel), Warum: strings.TrimSpace(warum)}, "laib", time.Now())
		return list, fmt.Sprintf("aufgabe add #%d %s", a.ID, a.Titel), nil
	}) != nil {
		return false
	}
	q, err := proposal.CreateFromEdit(ctx, env.Paths.State, cli.InboxDir(env), p.Actor, p.Reason, proposal.Edit{Set: p.Intent.Set}, JudgeFor(st, p.Actor))
	if err != nil {
		return false
	}
	q.Anchor, q.WartetAuf, q.Freigabe = p.Anchor, []string{fmt.Sprintf("aufgabe:%d", a.ID)}, p.DecidedBy
	proposal.Save(cli.InboxDir(env), q)
	p.Refiled, p.Because = q.ID, fmt.Sprintf("wartet auf Box-Aufgabe #%d „%s“; ist sie erledigt, wendet Laib die Änderung von selbst an (Vorschlag #%d)", a.ID, a.Titel, q.ID)
	proposal.Save(cli.InboxDir(env), p)
	cli.Record(env, st, journal.Entry{Actor: "laib", Verb: "approve", Reason: p.Reason, Result: "info", Detail: fmt.Sprintf("#%d wartet auf Box-Aufgabe #%d „%s“, neu als #%d, freigegeben von %s", p.ID, a.ID, a.Titel, q.ID, p.DecidedBy)})
	return true
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
		subject, err := RevertProposal(ctx, env, st, id, "", true)
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
func RevertProposal(ctx context.Context, env *cli.Env, st *state.State, id int, with string, drop bool) (string, error) {
	unlock := proposal.LockRepo(env.Paths.State) // beside an approve's merge (#609)
	defer func() { unlock() }()
	g := func(args ...string) (string, error) { return gitAs(ctx, env, args...) }
	merge, err := g("log", "--format=%H", "--grep=^approve #"+strconv.Itoa(id)+" by ", "-n", "1", "main")
	if err != nil || strings.TrimSpace(merge) == "" {
		return "", fmt.Errorf("keine Freigabe von #%d in der Geschichte des States: nichts zurückzunehmen", id)
	}
	merge = strings.TrimSpace(merge)
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
	unlock()
	unlock = func() {}
	if drop {
		if err := dropGone(ctx, env, st, fmt.Sprintf("rollback #%d", id)); err != nil {
			return "", err
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

func gitAs(ctx context.Context, env *cli.Env, args ...string) (string, error) {
	return sysd.Run(ctx, "git", append([]string{"-C", env.Paths.State, "-c", "user.name=laib", "-c", "user.email=laib@localhost"}, args...)...)
}

// dropGone drops the secrets of every service of was that the state on
// main no longer has (#41): what a revert took off keeps no secret.
func dropGone(ctx context.Context, env *cli.Env, was *state.State, why string) error {
	defer proposal.LockRepo(env.Paths.State)()
	after, err := cli.LoadState(env)
	if err != nil {
		return fmt.Errorf("der State nach %s lädt nicht: %w", why, err)
	}
	sec, err := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	n := len(sec)
	for _, name := range was.ServiceNames() {
		if _, ok := after.Services[name]; !ok && err == nil {
			for _, sdecl := range was.Services[name].Contract.Secrets {
				delete(sec, sdecl.Name)
			}
		}
	}
	if err != nil || len(sec) == n {
		return nil
	}
	if err := secrets.Save(env.Paths.Secrets, env.Paths.Key, sec); err != nil {
		return err
	}
	gitAs(ctx, env, "add", "-A")
	gitAs(ctx, env, "commit", "-q", "-m", why+": secrets of removed services dropped")
	return nil
}

// again puts proposal id's change on main again after a red run took it
// back (#1209): the revert of its newest rollback. It fails when the
// change builds on one that is not there.
func again(ctx context.Context, env *cli.Env, id int) error {
	defer proposal.LockRepo(env.Paths.State)()
	sha, _ := gitAs(ctx, env, "log", "--format=%H", "--grep=^rollback #"+strconv.Itoa(id)+"$", "-n", "1", "main")
	if sha = strings.TrimSpace(sha); sha == "" {
		return fmt.Errorf("#%d: keine Rücknahme in der Geschichte des States", id)
	}
	if out, err := gitAs(ctx, env, "revert", "--no-edit", sha); err != nil {
		gitAs(ctx, env, "revert", "--abort")
		return fmt.Errorf("#%d lässt sich allein nicht wieder einspielen: %s", id, out)
	}
	journal.Append(env.Paths.Journal, journal.Entry{Actor: env.Actor, Verb: "rollback", Result: "info", Detail: fmt.Sprintf("#%d wieder eingespielt, allein", id)})
	return nil
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
	var ways []proposal.Weg // ADR 0036
	if rerr == nil {
		ways = append(ways, proposal.Weg{Label: "Sicherung zurückspielen", Folge: "Die Daten kommen aus der Sicherung zurück; die von jetzt sichert Laib vorher.", Vorschlag: r.ID})
	}
	if f != nil {
		ways = append(ways, proposal.Weg{Label: "Nach vorne reparieren", Folge: "Die neue Version wird wieder aufgespielt, die Daten bleiben, wie das Update sie gelassen hat.", Vorschlag: f.ID})
	}
	for _, p := range batch {
		p.Because = strings.TrimSuffix(p.Because, ".") + ". " + told
		p.Wahl = ways
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
