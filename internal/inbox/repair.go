// Repairs (#441, owner 2026-10-19: "Das Interface ist jetzt der Chat"): a
// diag finding's repair verb becomes a proposal (laib repair, in the diag
// chore) that a chat hand or the Operator files and a human approves. It
// adds repairs/<time>.md, class human in the review; approving it runs the
// verb named there as the approver. Only the repairs laib offers run.

package inbox

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/state"
)

// RunVerb runs one laib verb as env's actor; main sets it to the verb table.
var RunVerb func(ctx context.Context, env *cli.Env, argv []string) int

// Repairable: the repairs diag offers. Nothing else runs from repairs/.
func Repairable(st *state.State, argv []string) bool {
	switch strings.Join(argv, " ") {
	case "apply", "backup", "backup check", "operator enable":
		return true
	}
	if len(argv) == 3 && argv[0] == "secret" && argv[1] == "zurueck" { // #665: a device-held secret goes back
		for _, svc := range st.Services {
			for _, s := range svc.Contract.Secrets {
				if s.Name == argv[2] && !s.External && !s.DataBound {
					return true
				}
			}
		}
		return false
	}
	if len(argv) == 2 && (argv[0] == "flash" || argv[0] == "retrain") { // #759: a flash unit writes firmware onto a device; #828: a retrain unit trains its model again
		return st.Services[argv[1]] != nil && slices.Contains(st.Services[argv[1]].Oneshots(), argv[1]+"-"+argv[0]+".service")
	}
	if len(argv) == 3 && argv[0] == "restore" { // #853: a red run's snapshot of a service with data back
		svc := st.Services[argv[2]]
		return hexID(argv[1]) && svc != nil && len(state.SnapshotPaths(st, svc)) > 0
	}
	if len(argv) == 2 && argv[0] == "rollback" { // #853: a red run's revert taken back, its change on again
		return hexID(argv[1])
	}
	return len(argv) == 2 && argv[0] == "restart" && st.Services[argv[1]] != nil
}

// hexID: a snapshot's or a commit's id, never a name or an option.
func hexID(s string) bool {
	return len(s) >= 7 && len(s) <= 64 && strings.Trim(s, "0123456789abcdef") == ""
}

// fileRepair files repairs laib offers itself (#853) as one proposal of
// laib's, one file per command line, so a human approves them with one
// tap: does is the card's headline, undo its way back, facts what it means.
func fileRepair(ctx context.Context, env *cli.Env, st *state.State, does, reason, undo string, facts, cmdlines []string) (*proposal.Proposal, error) {
	var patch strings.Builder
	stamp := time.Now().UTC().Format("2006-01-02T15-04-05.000000")
	for i, c := range cmdlines {
		name := fmt.Sprintf("repairs/%s-%d.md", stamp, i+1)
		fmt.Fprintf(&patch, "--- /dev/null\n+++ b/%s\n@@ -0,0 +1,3 @@\n+# %s\n+\n+%s\n", name, does, c)
	}
	p, err := proposal.CreateFromEdit(ctx, env.Paths.State, cli.InboxDir(env), "laib", reason+" — "+cmdlines[0], proposal.Edit{Patch: patch.String()}, JudgeFor(st, "laib"))
	if err != nil {
		return nil, err
	}
	p.Facts = append(p.Facts, facts...)
	return p, Anchor(cli.InboxDir(env), p, proposal.Anchor{Undo: undo, Does: does})
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
		if !strings.HasPrefix(last, "laib ") || !Repairable(st, argv) {
			cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "repair", Result: "red", Detail: fmt.Sprintf("#%d: %q ist keine Reparatur, die laib anbietet: nicht ausgeführt", p.ID, last)})
			code = cli.ExitRefused
			continue
		}
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "repair", Result: "info", Detail: fmt.Sprintf("#%d von %s: %s", p.ID, p.Actor, last)})
		// approved on the page like at the shell: after the work before it (#753, env.Wait)
		for until := time.Now().Add(env.Wait); apply.LockHolder(env.Paths) != "" && time.Now().Before(until); {
			time.Sleep(time.Second)
		}
		var errs strings.Builder
		run := *env
		run.ErrOut = io.MultiWriter(env.Errw(), &errs)
		if c := RunVerb(ctx, &run, argv); c != cli.ExitOK { // what failed, and why, in the journal and on the card (#753)
			lines := strings.Split(strings.TrimSpace(errs.String()), "\n")
			p.Because, code = "fehlgeschlagen: "+cmp.Or(strings.TrimPrefix(lines[len(lines)-1], "laib: "), fmt.Sprintf("exit %d", c)), c
			if argv[0] == "rollback" && c == cli.ExitRed { // #853: forward, and still red
				p.Because = "Die neue Version ist wieder aufgespielt, läuft aber noch nicht richtig. " + onward(st, true) + " Die Sicherung liegt weiter bereit."
			}
			proposal.Save(cli.InboxDir(env), p)
			cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "repair", Result: "red", Detail: fmt.Sprintf("#%d: %s %s", p.ID, last, p.Because)})
		}
	}
	return code
}
