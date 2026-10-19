// Repair: `laib repair <probe> [service]` (#441) files a finding's repair as a
// proposal; internal/inbox runs it once a human approves.

package diag

import (
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
