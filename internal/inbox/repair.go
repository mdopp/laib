// Repairs (#441, owner 2026-10-19: "Das Interface ist jetzt der Chat"): a
// diag finding's repair verb becomes a proposal (laib repair, in the diag
// chore) that a chat hand or the Operator files and a human approves. It
// adds repairs/<time>.md, class human in the review; approving it runs the
// verb named there as the approver. Only the repairs laib offers run.

package inbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

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
	if len(argv) == 2 && argv[0] == "flash" { // #759: a service with a flash unit, which writes firmware onto a device
		return st.Services[argv[1]] != nil && slices.Contains(st.Services[argv[1]].Oneshots(), argv[1]+"-flash.service")
	}
	return len(argv) == 2 && argv[0] == "restart" && st.Services[argv[1]] != nil
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
		if c := RunVerb(ctx, env, argv); c != cli.ExitOK {
			code = c
		}
	}
	return code
}
