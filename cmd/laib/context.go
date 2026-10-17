package main

import (
	"context"
	"fmt"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/pages"
)

func cmdContext(ctx context.Context, env *cliEnv, args []string) int {
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	r, rows := pages.CollectStatus(ctx, env, st)
	es, _ := journal.Read(env.Paths.Journal, time.Now().Add(-24*time.Hour))
	if env.JSON {
		return cli.Emit(env, map[string]any{"ready": r, "services": rows, "journal": es, "domain": st.Box.Domain})
	}
	fmt.Fprintf(env.W(), "# Laib context · %s · %s\n\n", st.Box.Domain, r.State)
	fmt.Fprintln(env.W(), "## Services")
	for _, row := range rows {
		fmt.Fprintf(env.W(), "- %s: %s\n", row.Name, row.State)
	}
	fmt.Fprintln(env.W(), "\n## Journal, letzte 24h")
	for _, e := range es {
		fmt.Fprintf(env.W(), "- %s %s %s %s %s %s\n", e.T.Format("15:04"), e.Actor, e.Verb, e.Service, e.Result, e.Reason)
	}
	fmt.Fprintln(env.W(), "\n## Read next\n- docs/DESIGN.md, docs/INTERFACE.md, docs/STATE.md in the laib repo\n- `laib plan` before any change; `laib propose` if your hand may not apply")
	return cli.ExitOK
}
