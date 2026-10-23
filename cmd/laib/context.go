package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/pages"
	"github.com/mdopp/laib/internal/proposal"
)

// cmdContext is the starter bundle (#579): by default an agent's first look,
// well under 20,000 characters — readiness, the services that are not
// green, the open proposals, the journal's newest lines without the looks,
// each cut to a line. --full is the whole bundle; --since narrows the journal.
func cmdContext(ctx context.Context, env *cliEnv, args []string) int {
	since, full := 24*time.Hour, slices.Contains(args, "--full")
	if i := slices.Index(args, "--since"); i >= 0 && i+1 < len(args) {
		d, err := time.ParseDuration(args[i+1])
		if err != nil {
			return env.Fail(err)
		}
		since = d
	}
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	r, rows := pages.CollectStatus(ctx, env, st)
	es, _ := journal.Read(env.Paths.Journal, time.Now().Add(-since))
	omitted := 0
	if !full {
		rows = slices.DeleteFunc(rows, func(s pages.ServiceStatus) bool { return s.State == "grün" })
		es, omitted = shortJournal(es)
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	for _, p := range open {
		p.Reasons, p.Warnings, p.Facts = nil, nil, nil
	}
	lessons := lessonsOf(env.Paths.State)
	upstream := map[string][]string{} // where each service's upstream lives (#544)
	for name, svc := range st.Services {
		up := svc.Contract.Upstream
		if len(up) == 0 {
			up = templateUpstream(env, svc.Contract.Template.Name)
		}
		if len(up) > 0 {
			upstream[name] = up
		}
	}
	if env.JSON {
		return cli.Emit(env, map[string]any{"ready": r, "services": rows, "journal": es, "omitted": omitted, "open": open, "domain": st.Box.Domain, "lessons": lessons, "upstream": upstream})
	}
	fmt.Fprintf(env.W(), "# Laib context · %s · %s\n\n", st.Box.Domain, r.State)
	fmt.Fprintf(env.W(), "## Services (%d grün, %d gelb, %d rot)\n", r.Green, r.Yellow, r.Red)
	for _, row := range rows {
		fmt.Fprintf(env.W(), "- %s: %s %s\n", row.Name, row.State, row.Detail)
	}
	fmt.Fprintln(env.W(), "\n## Offene Vorschläge")
	for _, p := range open {
		fmt.Fprintf(env.W(), "- #%d %s (%s)\n", p.ID, p.Reason, p.Actor)
	}
	fmt.Fprintf(env.W(), "\n## Journal, seit %s\n", since)
	for _, e := range es {
		fmt.Fprintf(env.W(), "- %s %s %s %s %s %s\n", e.T.Format("15:04"), e.Actor, e.Verb, e.Service, e.Result, e.Reason)
	}
	if omitted > 0 {
		fmt.Fprintf(env.W(), "- … %d Einträge ausgelassen: laib context --full, laib journal\n", omitted)
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

// shortJournal: the newest 30 entries without the looks (reads), reason and
// detail cut to a line each; and how many it left out.
func shortJournal(es []journal.Entry) ([]journal.Entry, int) {
	n := len(es)
	es = slices.DeleteFunc(es, func(e journal.Entry) bool { return e.Verb == "look" })
	es = es[max(0, len(es)-30):]
	cut := func(s string, n int) string {
		if r := []rune(s); len(r) > n {
			return string(r[:n]) + "…"
		}
		return s
	}
	for i := range es {
		es[i].Reason, es[i].Detail = cut(es[i].Reason, 160), cut(es[i].Detail, 100)
	}
	return es, n - len(es)
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

// templateUpstream: a service added before its template named its upstream
// (#544) reads it from the template, so no template update is needed.
func templateUpstream(env *cliEnv, name string) []string {
	var c struct{ Upstream []string }
	if tpl, err := inbox.TemplateFS(env, name); err == nil {
		b, _ := fs.ReadFile(tpl, "laib.yml")
		yaml.Unmarshal(b, &c)
	}
	return c.Upstream
}
