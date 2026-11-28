package inbox

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/review"
	"github.com/mdopp/laib/internal/state"
)

// Seen (ADR 0037) is the newest digest laib updates found for a service's
// value key and since when it is the newest; main sets it (diag.Seen).
var Seen = func(home, service, key string) (string, time.Time) { return "", time.Time{} }

// Soak is how long a third-party image's new digest waits before it may
// apply itself: the template's soak in days, else three (ADR 0037).
func Soak(st *state.State, service string) time.Duration {
	days := 3
	if s := st.Services[service]; s != nil && s.Contract.Soak > 0 {
		days = s.Contract.Soak
	}
	return time.Duration(days) * 24 * time.Hour
}

// routineWhy is "" when the Operator's routine proposal p may apply itself
// (ADR 0037): the switch is on, and p brings only new digests of images
// that services already run — nothing the review names but those — each
// the newest laib updates found, and a third-party one waited its soak
// unless it is a security fix. Anything that changes rights (a route, a
// key, a host script, a port, policy or box.yml, a template) never passes:
// its reason is not an image's. Else why not, in words.
func routineWhy(st *state.State, p *proposal.Proposal, home string, now time.Time) string {
	if st.Box.Updates.Auto == "aus" {
		return "die Updates spielen sich nicht selbst ein (box.yml updates.auto: aus)"
	}
	if proposal.Hand(p.Actor) != "ops" || p.Kind != "values" || len(p.Changes) == 0 || len(p.Mints)+len(p.Adds)+len(p.Removes) > 0 {
		return "nur der Operator spielt so ein, und nur neue Images laufender Dienste"
	}
	for _, r := range slices.Concat(p.Reasons, p.Warnings) {
		if !strings.HasPrefix(r, review.NewImage) {
			return "ändert mehr als Images: " + r
		}
	}
	for _, f := range p.Files {
		if !strings.HasPrefix(f, "services/") || !strings.HasSuffix(f, "/values.yml") {
			return "ändert mehr als Images: " + f
		}
	}
	for _, c := range p.Changes {
		_, digest, _ := strings.Cut(c.New, "@")
		if !imageChange(c) || c.Pin {
			return "ändert mehr als Images: " + c.Service + " " + c.Key
		}
		if want, since := Seen(home, c.Service, c.Key); want != digest {
			return c.Service + ": nicht der neueste Stand, den laib updates gefunden hat"
		} else if until := since.Add(Soak(st, c.Service)); !strings.HasPrefix(c.New, "ghcr.io/mdopp/") && p.Routine != "sicherheit" && now.Before(until) {
			return c.Service + " wartet noch bis " + until.Local().Format("02.01. 15:04")
		}
	}
	return ""
}

// routine is --routine normal|sicherheit (ADR 0037): p applies itself when
// routineWhy lets it, approved by laib as the dial does; else it stays a
// card for a person, which says why it did not. auto: it was decided here.
func routine(ctx context.Context, env *cli.Env, st *state.State, p *proposal.Proposal, word string) (auto bool, code int) {
	p.Routine = word
	why := routineWhy(st, p, env.Paths.Home, time.Now())
	if word != "normal" && word != "sicherheit" || env.Ceiling != "" {
		why = "--routine ist normal oder sicherheit, von keinem Programm hinter der Tür"
	}
	if why == "" {
		return true, AutoApprove(ctx, env, p)
	}
	p.Routine, p.Reasons = "", append(p.Reasons, "nicht von selbst eingespielt: "+why+" (ADR 0037)")
	if err := proposal.Save(cli.InboxDir(env), p); err != nil {
		return true, env.Fail(err)
	}
	return false, cli.ExitOK
}

// how laib approved p itself, for the journal: by the dial, or as a routine.
func how(p *proposal.Proposal) string {
	if p.Routine != "" {
		return "als Routine (" + p.Routine + ", ADR 0037)"
	}
	return "per autonomy"
}
