package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	lessons := lessonsOf(env.Paths.State)
	if env.JSON {
		return cli.Emit(env, map[string]any{"ready": r, "services": rows, "journal": es, "domain": st.Box.Domain, "lessons": lessons})
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
	if len(lessons) > 0 {
		fmt.Fprintln(env.W(), "\n## Lehren")
		for _, l := range lessons {
			fmt.Fprintln(env.W(), l)
		}
	}
	fmt.Fprintln(env.W(), "\n## Read next\n- docs/DESIGN.md, docs/INTERFACE.md, docs/STATE.md in the laib repo\n- `laib plan` before any change; `laib propose` if your hand may not apply")
	return cli.ExitOK
}

// lessonsOf: the lessons the owner let in (state repo lessons/*.md, #544),
// each as it stands, in name order, together at most 4000 bytes.
func lessonsOf(state string) []string {
	files, _ := filepath.Glob(filepath.Join(state, "lessons", "*.md"))
	out, size := []string{}, 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if size += len(b); err != nil || size > 4000 {
			break
		}
		out = append(out, string(b))
	}
	return out
}
