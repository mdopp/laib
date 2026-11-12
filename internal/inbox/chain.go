package inbox

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mdopp/laib/internal/aufgaben"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// Chained approvals (#1194): a proposal names what it waits for. An
// approve while something is missing holds it, approved in advance;
// Release, at the start of every run, approves it as that person once
// nothing is missing, oldest first; a proposal it waits for counts once a
// run applied it, so a chain applies in its order.

var waitRe = regexp.MustCompile(`^((vorschlag|aufgabe):[1-9][0-9]{0,6}|version:v?[0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,4}|schluessel:[A-Z][A-Z0-9_]{0,63})$`)

// Wait is one thing a proposal still waits for, in the owner's words, with
// the page where it is done. Nie: it will not come; the card stays open.
type Wait struct {
	Text string `json:"text"`
	Link string `json:"link,omitempty"`
	Nie  bool   `json:"nie,omitempty"`
}

// checkWaits refuses a wait laib cannot measure.
func checkWaits(st *state.State, ws []string) error {
	for _, w := range ws {
		kind, name, _ := strings.Cut(w, ":")
		if !waitRe.MatchString(w) || kind == "schluessel" && keyLabel(st, name) == "" {
			return fmt.Errorf("--wartet %q: vorschlag:<nr>, aufgabe:<nr>, version:<x.y.z> oder schluessel:<NAME> eines Schlüssels, den der Besitzer einträgt", w)
		}
	}
	return nil
}

// Waits is what p still waits for; none when it may be approved.
func Waits(env *cli.Env, st *state.State, p *proposal.Proposal) []Wait {
	var out []Wait
	for _, w := range p.WartetAuf {
		kind, arg, _ := strings.Cut(w, ":")
		n, _ := strconv.Atoi(arg)
		switch kind {
		case "vorschlag":
			out = append(out, waitProposal(env, st, n)...)
		case "version":
			if !atLeast(cli.Version, arg) {
				out = append(out, Wait{Text: "wartet auf Laib " + strings.TrimPrefix(arg, "v") + " (läuft: " + cli.Version + ")"})
			}
		case "schluessel":
			if sec, _ := secrets.Load(env.Paths.Secrets, env.Paths.Key); sec[arg] == "" {
				out = append(out, Wait{Text: "wartet auf den Schlüssel „" + cmp.Or(keyLabel(st, arg), arg) + "“", Link: render.KeyLink(st, arg)})
			}
		case "aufgabe":
			list, _ := aufgaben.Load(env.Paths.State)
			i := slices.IndexFunc(list, func(a aufgaben.Aufgabe) bool { return a.ID == n })
			if i < 0 {
				out = append(out, Wait{Text: fmt.Sprintf("wartet auf Box-Aufgabe #%d, die es nicht gibt: diesen ablehnen oder neu vorschlagen", n), Nie: true})
			} else if a := list[i]; a.Open() {
				out = append(out, Wait{Text: fmt.Sprintf("wartet auf Box-Aufgabe #%d „%s“", n, a.Titel), Link: taskLink(st, a)})
			}
		}
	}
	return out
}

// waitProposal: proposal n, or what replaced it, is not applied yet.
func waitProposal(env *cli.Env, st *state.State, n int) []Wait {
	q, err := proposal.Get(cli.InboxDir(env), n)
	for err == nil && q.Refiled != 0 { // superseded or computed again: its successor
		q, err = proposal.Get(cli.InboxDir(env), q.Refiled)
	}
	if err != nil {
		return []Wait{{Text: fmt.Sprintf("wartet auf Vorschlag #%d, den es nicht gibt: diesen ablehnen oder neu vorschlagen", n), Nie: true}}
	}
	link := render.PageLink(st, fmt.Sprintf("/inbox#%d", q.ID))
	gone := map[string]string{"rejected": "abgelehnt", "stale": "veraltet"}[q.Status]
	if q.Outcome == "rolled-back" {
		gone = "zurückgenommen"
	}
	if gone != "" {
		return []Wait{{Text: fmt.Sprintf("wartet auf Vorschlag #%d, der %s ist: diesen ablehnen oder neu vorschlagen", q.ID, gone), Link: link, Nie: true}}
	}
	if q.Status == "open" || q.Outcome == "waiting" {
		return []Wait{{Text: fmt.Sprintf("wartet auf Vorschlag #%d", q.ID), Link: link}}
	}
	return nil
}

// keyLabel is an owner's key's label; "" for any other name.
func keyLabel(st *state.State, name string) string {
	for _, o := range render.OwnerSecrets(st) {
		if o.Name == name {
			return cmp.Or(o.Owner.Label, name)
		}
	}
	return ""
}

func taskLink(st *state.State, a aufgaben.Aufgabe) string {
	if a.Weg != nil && a.Weg.Schluessel != "" {
		return render.KeyLink(st, a.Weg.Schluessel)
	}
	return ""
}

var versionRe = regexp.MustCompile(`([0-9]+)\.([0-9]+)\.([0-9]+)`)

// atLeast: version have is want or newer; a build without a version is not.
func atLeast(have, want string) bool {
	pad := func(v string) string {
		m := append(versionRe.FindStringSubmatch(v), "", "", "", "")
		return fmt.Sprintf("%05s%05s%05s", m[1], m[2], m[3])
	}
	return versionRe.MatchString(have) && pad(have) >= pad(want)
}

// hold: an approve of a proposal that still waits keeps it open, approved
// in advance by by (Freigabe); it says so. held is false when nothing is missing.
func hold(env *cli.Env, st *state.State, p *proposal.Proposal, by string) (held bool, code int) {
	ws := Waits(env, st, p)
	if len(ws) == 0 {
		return false, cli.ExitOK
	}
	if proposal.Own(p, by) {
		fmt.Fprintln(env.Errw(), "laib:", proposal.ErrOwnProposal)
		return true, cli.ExitRefused
	}
	p.Freigabe = by
	if err := proposal.Save(cli.InboxDir(env), p); err != nil {
		return true, env.Fail(err)
	}
	texts := make([]string, len(ws))
	for i, w := range ws {
		texts[i] = w.Text
	}
	cli.Record(env, st, journal.Entry{Actor: by, Verb: "approve", Reason: p.Reason, Result: "info", Detail: fmt.Sprintf("#%d von %s freigegeben, %s", p.ID, p.Actor, strings.Join(texts, "; "))})
	fmt.Fprintf(env.W(), "Vorschlag #%d freigegeben: Laib wendet ihn an, sobald nichts mehr fehlt (%s).\n", p.ID, strings.Join(texts, "; "))
	return true, cli.ExitOK
}

// WaitsOf is Waits for a listing or a card: none for a decided proposal.
func WaitsOf(env *cli.Env, p *proposal.Proposal) []Wait {
	st, err := cli.LoadState(env)
	if err != nil || p.Status != "open" {
		return nil
	}
	return Waits(env, st, p)
}

// Release approves every held proposal that no longer waits, oldest first,
// as whoever approved it; Decide checks the hand again.
func Release(ctx context.Context, env *cli.Env) {
	f, _ := os.OpenFile(cli.InboxDir(env)+".release.lock", os.O_CREATE|os.O_RDWR, 0o644)
	syscall.Flock(int(f.Fd()), syscall.LOCK_EX) // one pass at a time: a second never approves what the first did; no file, no lock
	defer f.Close()
	held, _ := proposal.List(cli.InboxDir(env), true)
	st, err := cli.LoadState(env)
	for _, p := range slices.Backward(held) {
		if err != nil || p.Freigabe == "" || len(Waits(env, st, p)) > 0 {
			continue
		}
		var why strings.Builder
		as := &cli.Env{Paths: env.Paths, Actor: p.Freigabe, Out: io.Discard, ErrOut: &why}
		if Decide(ctx, as, []string{strconv.Itoa(p.ID)}, true) != cli.ExitOK {
			cli.Record(env, st, journal.Entry{Actor: "laib", Verb: "approve", Reason: p.Reason, Result: "red", Detail: fmt.Sprintf("#%d war freigegeben, nichts fehlt mehr, aber: %s", p.ID, strings.TrimSpace(why.String()))})
		}
	}
}

func printWaits(env *cli.Env, p *proposal.Proposal) {
	for _, w := range WaitsOf(env, p) {
		fmt.Fprintf(env.W(), "     %s %s\n", w.Text, w.Link)
	}
}

// supersede closes each older open values proposal whose every value p
// sets again (#1194): approved after p, it would take p back.
func supersede(ctx context.Context, env *cli.Env, st *state.State, p *proposal.Proposal) {
	if p.Kind != "values" {
		return
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	for _, o := range open {
		if o.ID >= p.ID || o.Kind != "values" || len(o.Changes) == 0 || slices.ContainsFunc(o.Changes, func(c proposal.Change) bool {
			return !slices.ContainsFunc(p.Changes, func(d proposal.Change) bool { return d.Service == c.Service && d.Key == c.Key })
		}) {
			continue
		}
		sysd.Run(ctx, "git", "-C", env.Paths.State, "branch", "-q", "-D", o.Branch)
		o.Status, o.StaleReason, o.Refiled, o.Decided, o.DecidedBy = "stale", fmt.Sprintf("abgelöst durch #%d, der dieselben Werte neuer setzt", p.ID), p.ID, time.Now().UTC(), "laib"
		proposal.Save(cli.InboxDir(env), o)
		cli.Record(env, st, journal.Entry{Actor: "laib", Verb: "reject", Reason: o.Reason, Result: "info", Detail: fmt.Sprintf("#%d abgelöst durch #%d", o.ID, p.ID)})
	}
}
