// The wish path (#56, ADR 0023 §6): the Operator's wish as a proposal, and
// filing approved wishes and escalations as tickets in box.yml intake.repo
// through laib-haushalt (#797), which serves `laib wish` itself.

package inbox

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/state"
)

// Issue files a ticket in box.yml intake.repo and returns its URL; an
// empty intake means "not configured" (ok=false, no error). laib-haushalt
// files it behind the verb door with its own token (#797): the kernel reads
// no GitHub token. Set by cmd/laib.
var Issue func(ctx context.Context, env *cli.Env, st *state.State, title, body, label string) (url string, ok bool, err error)

// WishLabel is the label a wish gets.
func WishLabel(st *state.State) string { return cmp.Or(st.Box.Intake.Label, "wunsch") }

// WishProposal: the Operator's wish waits for the admin (ADR 0023 §6). It
// is a proposal that adds wishes/<time>.md — class human in the review —
// and approving it files the intake issue (FileWishes). Every other hand's
// wish laib-haushalt files behind the verb door (#797).
func WishProposal(ctx context.Context, env *cli.Env, st *state.State, title, body string) int {
	name := "wishes/" + time.Now().UTC().Format("2006-01-02T15-04-05.000000") + ".md"
	lines := strings.Split("# "+title+"\n\n"+body, "\n")
	patch := fmt.Sprintf("--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n+%s\n", name, len(lines), strings.Join(lines, "\n+"))
	p, err := proposal.CreateFromEdit(ctx, env.Paths.State, cli.InboxDir(env), env.Actor, title, proposal.Edit{Patch: patch}, JudgeFor(st, env.Actor))
	if err != nil {
		return env.Fail(err)
	}
	cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "wish", Result: "info", Reason: title, Detail: fmt.Sprintf("#%d wartet auf Freigabe", p.ID)})
	if env.JSON {
		return cli.Emit(env, p)
	}
	fmt.Fprintf(env.W(), "Vorschlag #%d angelegt: nach der Freigabe wird daraus ein Ticket.\n", p.ID)
	return cli.ExitOK
}

// FileWishes: the wishes an approved proposal adds become intake issues.
func FileWishes(ctx context.Context, env *cli.Env, st *state.State, p *proposal.Proposal) {
	for _, f := range p.Files {
		b, err := os.ReadFile(filepath.Join(env.Paths.State, f))
		if !strings.HasPrefix(f, "wishes/") || err != nil {
			continue
		}
		title, body, _ := strings.Cut(string(b), "\n")
		title = strings.TrimPrefix(title, "# ")
		body = strings.TrimSpace(body) + fmt.Sprintf("\n\nFreigegeben von %s (#%d).", env.Actor, p.ID)
		entry := journal.Entry{Actor: env.Actor, Verb: "wish", Result: "info", Reason: title}
		url, ok, err := Issue(ctx, env, st, title, body, WishLabel(st))
		switch {
		case err != nil:
			entry.Result, entry.Detail = "red", err.Error()
		case !ok:
			entry.Detail = "kein Intake in box.yml: der Wunsch bleibt in " + f
		default:
			entry.Detail = url
		}
		cli.Record(env, st, entry)
	}
}
