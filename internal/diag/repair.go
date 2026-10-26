// Repair: `laib repair <probe> [service]` (#441) files a finding's repair as a
// proposal; internal/inbox runs it once a human approves.

package diag

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/state"
)

func Repair(ctx context.Context, env *cli.Env, args []string) int {
	if len(args) < 1 || len(args) > 2 {
		return env.Fail(errors.New("usage: laib repair <probe> [service]   (laib diag zeigt, was sich reparieren lässt)"))
	}
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	if !policy.Allowed(st.Policy, proposal.Hand(env.Actor), "propose") {
		return cli.Refused(env, env.Actor+" may not propose")
	}
	svc := ""
	if len(args) == 2 {
		svc = args[1]
	}
	var f *Finding
	for _, x := range Probes(ctx, env, st, svc) {
		if x.Probe == args[0] && x.Service == svc && inbox.Repairable(st, x.Repair) {
			f = &x
			break
		}
	}
	if f == nil {
		return env.Fail(fmt.Errorf("keine Reparatur für %s %s: laib diag zeigt, was sich reparieren lässt", args[0], svc))
	}
	cmdline := "laib " + strings.Join(f.Repair, " ")
	reason := "Reparieren: " + f.Detail + " — " + cmdline
	name := "repairs/" + time.Now().UTC().Format("2006-01-02T15-04-05.000000") + ".md"
	patch := fmt.Sprintf("--- /dev/null\n+++ b/%s\n@@ -0,0 +1,3 @@\n+# %s\n+\n+%s\n", name, reason, cmdline)
	p, err := proposal.CreateFromEdit(ctx, env.Paths.State, cli.InboxDir(env), env.Actor, reason, proposal.Edit{Patch: patch}, inbox.JudgeFor(st, env.Actor))
	if err == nil && proposal.VerbTier[f.Repair[0]] == "destroy" { // #665: only the Inbox approves it
		p.Tier = "destroy"
		err = proposal.Save(cli.InboxDir(env), p)
	}
	if f.Effect != "" { // #759: what the card tells the owner happens
		p.Facts = append(p.Facts, f.Effect)
	}
	if err == nil { // the finding it answers is the one laib measures afterwards (#544)
		undo := cmp.Or(f.Undo, "nichts zurückzunehmen: laib führt nur "+cmdline+" aus")
		err = inbox.Anchor(cli.InboxDir(env), p, proposal.Anchor{Finding: key(*f), Evidence: f.Detail, Undo: undo})
	}
	if err != nil {
		return env.Fail(err)
	}
	cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "repair", Service: svc, Result: "info", Reason: reason, Detail: fmt.Sprintf("#%d wartet auf Freigabe", p.ID)})
	if env.JSON {
		return cli.Emit(env, p)
	}
	fmt.Fprintf(env.W(), "Vorschlag #%d angelegt: nach der Freigabe durch einen Menschen läuft %s.\n", p.ID, cmdline)
	return cli.ExitOK
}

// key is a finding's identity for an anchor (#544): probe and service.
func key(f Finding) string { return strings.TrimSpace(f.Probe + " " + f.Service) }

// Present: diag shows the finding now (#544).
func Present(ctx context.Context, env *cli.Env, st *state.State, finding string) bool {
	probe, svc, _ := strings.Cut(finding, " ")
	for _, f := range Probes(ctx, env, st, svc) {
		if f.Probe == probe && key(f) == finding {
			return true
		}
	}
	return false
}

// settle records, after a look at the whole box, what came of each approved
// proposal (#544): its finding gone is effective, still there after
// proposal.Window no effect. Journaled once each.
func settle(env *cli.Env, st *state.State, fs []Finding) {
	now := map[string]bool{}
	for _, f := range fs {
		now[key(f)] = true
	}
	for _, p := range proposal.Settle(cli.InboxDir(env), func(k string) bool { return now[k] }, time.Now().UTC()) {
		what := map[string]string{"effective": "wirksam: der Befund ist weg", "no-effect": "ohne Wirkung: der Befund ist noch da"}[p.Outcome]
		cli.Record(env, st, journal.Entry{Actor: "laib", Verb: "outcome", Reason: p.Reason, Result: "info", Detail: fmt.Sprintf("#%d von %s %s (%s)", p.ID, p.Actor, what, p.Finding)})
	}
}
