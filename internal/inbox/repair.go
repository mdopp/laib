// Repairs (#441, owner 2026-10-19: "Das Interface ist jetzt der Chat"): a
// diag finding's repair verb becomes a proposal that a chat hand or the
// Operator files and a human approves. It adds repairs/<time>.md, class
// human in the review; approving it runs the verb named there as the
// approver. Only the repairs laib offers run.

package inbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/state"
)

// RunVerb runs one laib verb as env's actor; main sets it to the verb table.
var RunVerb func(ctx context.Context, env *cli.Env, argv []string) int

// repairable: the repairs diag offers. Nothing else runs from repairs/.
func repairable(st *state.State, argv []string) bool {
	switch strings.Join(argv, " ") {
	case "apply", "backup", "backup check", "operator enable":
		return true
	}
	return len(argv) == 2 && argv[0] == "restart" && st.Services[argv[1]] != nil
}

// Repair: `laib repair <probe> [service]`.
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
	var f *diag.Finding
	for _, x := range diag.Probes(ctx, env, st, svc) {
		if x.Probe == args[0] && x.Service == svc && repairable(st, x.Repair) {
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
	p, err := proposal.CreateFromEdit(ctx, env.Paths.State, cli.InboxDir(env), env.Actor, reason, proposal.Edit{Patch: patch}, JudgeFor(st, env.Actor))
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

// RunRepairs runs the repairs an approved proposal adds, as the approver.
func RunRepairs(ctx context.Context, env *cli.Env, st *state.State, p *proposal.Proposal) int {
	code := cli.ExitOK
	for _, f := range p.Files {
		if !strings.HasPrefix(f, "repairs/") {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(env.Paths.State, f))
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		last := lines[len(lines)-1]
		argv := strings.Fields(strings.TrimPrefix(last, "laib "))
		if !strings.HasPrefix(last, "laib ") || !repairable(st, argv) {
			cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "repair", Result: "red", Detail: fmt.Sprintf("#%d: %q ist keine Reparatur, die laib anbietet: nicht ausgeführt", p.ID, last)})
			code = cli.ExitRefused
			continue
		}
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "repair", Result: "info", Detail: fmt.Sprintf("#%d von %s: %s", p.ID, p.Actor, last)})
		if c := RunVerb(ctx, env, argv); c != cli.ExitOK {
			code = c
		}
	}
	return code
}
