package registry

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
)

func TemplateCmd(ctx context.Context, env *cli.Env, args []string) int {
	if len(args) == 0 {
		return env.Fail(errors.New("usage: laib template lint <dir> | list | sync | update <service>"))
	}
	switch args[0] {
	case "lint":
		if len(args) < 2 {
			return env.Fail(errors.New("usage: laib template lint <dir>"))
		}
		return LintTemplate(env, args[1])
	case "list":
		st, err := cli.LoadState(env)
		if err != nil {
			return env.Fail(err)
		}
		avail := Available(env)
		if env.JSON {
			return cli.Emit(env, map[string]any{"available": avail, "installed": st.ServiceNames()})
		}
		fmt.Fprintln(env.W(), "installiert:")
		for _, name := range st.ServiceNames() {
			t := st.Services[name].Contract.Template
			fmt.Fprintf(env.W(), "  %-20s %s/%s v%d\n", name, t.Registry, t.Name, t.Version)
		}
		fmt.Fprintln(env.W(), "verfügbar:")
		for _, name := range SortedKeys(avail) {
			fmt.Fprintf(env.W(), "  %-20s %s\n", name, strings.Join(avail[name], ", "))
		}
		return cli.ExitOK
	case "sync":
		st, err := cli.LoadState(env)
		if err != nil {
			return env.Fail(err)
		}
		lines, err := Sync(ctx, env, st)
		for _, l := range lines {
			fmt.Fprintln(env.W(), "registry:", l)
		}
		if err != nil {
			return env.Fail(err)
		}
		journal.Append(env.Paths.Journal, journal.Entry{Actor: env.Actor, Verb: "template", Result: "info", Detail: "sync " + strings.Join(lines, "; ")})
		if len(args) > 1 && args[1] == "--propose" {
			// Every installed service whose template changed gets an update
			// proposal; the dial decides whether laib applies it itself.
			code := cli.ExitOK
			for _, name := range st.ServiceNames() {
				if c := Update(ctx, env, st, name); c != cli.ExitOK && c != cli.ExitUsage {
					code = c
				}
			}
			return code
		}
		return cli.ExitOK
	case "update":
		if len(args) < 2 {
			return env.Fail(errors.New("usage: laib template update <service>"))
		}
		st, err := cli.LoadState(env)
		if err != nil {
			return env.Fail(err)
		}
		if !policy.Allowed(st.Policy, proposal.Hand(env.Actor), "propose") {
			return cli.Refused(env, env.Actor+" may not propose")
		}
		return Update(ctx, env, st, args[1])
	}
	return env.Fail(fmt.Errorf("unknown template subcommand %q", args[0]))
}
