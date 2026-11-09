// Private services (ADR 0032, owner 2026-11-09): a template marks the
// family's data private. Into a sealed service (the archive) no verb looks,
// for no hand; into an approval service an agent looks only for an hour
// after a human approved an Einblick for it, one Inbox card per occasion,
// and the journal keeps the size of what came out, never the text.

package envelope

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/state"
)

// GrantFor is how long an approved Einblick lets its hand look (ADR 0032).
const GrantFor = time.Hour

// gate decides whether verb may reach into svc: ExitOK, or the refusal.
func gate(ctx context.Context, env *cli.Env, st *state.State, svc *state.Service, verb, what string) int {
	hand := proposal.Hand(env.Actor)
	switch {
	case svc.Contract.Private == "sealed":
		return cli.Refused(env, proposal.Named(svc.Name)+" ist versiegelt: dort sieht niemand hinein, kein Agent und kein Mensch über Laib (ADR 0032). Den Zustand zeigen laib status und laib logs")
	case svc.Contract.Private != "approval" || hand == "human" || verb == "write":
		return cli.ExitOK
	}
	all, _ := proposal.List(cli.InboxDir(env), false)
	suffix := "-" + hand + "-" + svc.Name + ".md"
	for _, p := range all {
		if !p.Boxless() || !strings.HasPrefix(p.Reason, "Einblick:") || !strings.HasSuffix(strings.Join(p.Files, " "), suffix) {
			continue
		}
		switch {
		case p.Status == "approved" && proposal.Hand(p.DecidedBy) == "human" && time.Since(p.Decided) < GrantFor:
			return cli.ExitOK
		case p.Status == "open":
			return cli.Refused(env, fmt.Sprintf("%s ist privat: das wartet auf Freigabe #%d im Eingang; danach eine Stunde lang", svc.Name, p.ID))
		}
	}
	if !policy.Allowed(st.Policy, hand, "propose") {
		return cli.Refused(env, svc.Name+" ist privat: hineinsehen nur mit einer Freigabe, und "+env.Actor+" darf keine erbitten")
	}
	does := proposal.HandName(hand, false) + " darf eine Stunde lang in " + proposal.Named(svc.Name) + " hineinsehen"
	name := "einblicke/" + time.Now().UTC().Format("2006-01-02T15-04-05.000000") + suffix
	cmdline := "laib einblick " + verb + " " + svc.Name + " " + what
	patch := fmt.Sprintf("--- /dev/null\n+++ b/%s\n@@ -0,0 +1,3 @@\n+# %s\n+\n+%s\n", name, does, cmdline)
	p, err := proposal.CreateFromEdit(ctx, env.Paths.State, cli.InboxDir(env), env.Actor, "Einblick: "+does+" — "+cmdline, proposal.Edit{Patch: patch}, inbox.JudgeFor(st, env.Actor))
	if err == nil {
		p.Does = does // the card's headline (#746)
		err = proposal.Save(cli.InboxDir(env), p)
	}
	if err != nil {
		return env.Fail(err)
	}
	return cli.Refused(env, fmt.Sprintf("%s ist privat: das wartet auf Freigabe #%d im Eingang; danach eine Stunde lang", svc.Name, p.ID))
}

// outputSize is what the journal keeps of a private service's output.
func outputSize(out string) string {
	return strings.Replace(fmt.Sprintf("%.1f KB Ausgabe", float64(len(out))/1024), ".", ",", 1)
}
