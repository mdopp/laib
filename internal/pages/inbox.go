package pages

import (
	"fmt"
	"html"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
)

// Inbox is the Inbox page: what the box does with what was approved and
// what came of it (#686), the open decision cards, what is stale.
func Inbox(env *cli.Env) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		all, err := proposal.List(cli.InboxDir(env), false)
		var b strings.Builder
		b.WriteString("<h1>Inbox</h1>" + filter)
		if err != nil {
			b.WriteString(failure("Die Box kann die Vorschläge gerade nicht ganz lesen. Lade die Seite in einer Minute noch einmal.", err.Error()))
		}
		domain, box := "", []string(nil)
		if st, err := cli.LoadState(env); err == nil {
			domain, box = st.Box.Domain, st.Warnings
		}
		list, open, n, busy, fresh := decided(all, cli.InboxDir(env), domain, apply.LockHolder(env.Paths) != "")
		if may, _ := oneByOne(approvable(env, open)); len(may) > 1 {
			fmt.Fprintf(&b, `<p><a href="/inbox/alle"><button class=go>Alle freigeben (%d)</button></a></p>`, len(may))
		}
		head := laeuft(list, busy)
		if n > 0 && (busy || fresh) {
			b.WriteString(head)
		}
		if len(open) == 0 {
			b.WriteString("<p>Keine offenen Vorschläge.</p>")
		}
		for _, p := range open {
			card(&b, r, env, p, domain, box)
		}
		if n > 0 && !busy && !fresh {
			b.WriteString(head)
		}
		b.WriteString(staleLines(all))
		page(w, "Inbox", b.String())
	}
}

// InboxStand is the block of what the box does for the live script (#686):
// the decided cards alone.
func InboxStand(env *cli.Env) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		all, _ := proposal.List(cli.InboxDir(env), false)
		domain := ""
		if st, err := cli.LoadState(env); err == nil {
			domain = st.Box.Domain
		}
		list, _, _, busy, _ := decided(all, cli.InboxDir(env), domain, apply.LockHolder(env.Paths) != "")
		fragment(w, laeuft(list, busy))
	}
}

// laeuft is the decided cards under their heading, by id; data-run while
// one runs, with its limit (#854).
func laeuft(list string, busy bool) string {
	if busy {
		return "<div id=laeuft data-run><h2>Was die Box gerade macht</h2>" + frist("Meist ist sie in ein, zwei Minuten fertig, mit Arbeit davor auch länger.") + "<div class=liste>" + list + "</div></div>"
	}
	return "<div id=laeuft><h2>Zuletzt entschieden</h2><div class=liste>" + list + "</div></div>"
}

// decided sorts the proposals into the cards of what was decided as they
// end (#686) — a tap that failed among them, where its card was, with its
// reason (#854) — and the open ones still to decide.
func decided(all []*proposal.Proposal, dir, domain string, locked bool) (cards string, open []*proposal.Proposal, n int, busy, fresh bool) {
	var list strings.Builder
	for _, p := range all {
		decision := DecisionState(p.ID)
		f, failed := failedTap(dir, p.ID)
		failed = failed && decision == ""
		if p.Status == "open" && decision == "" {
			open = append(open, p) // a failed tap's card keeps its buttons there
			if !failed {
				continue
			}
		}
		text, class, running := progress(p, decision, locked)
		done := text == "fertig"
		recent := failed || time.Since(p.Decided) < 15*time.Minute
		if !(running || failed || p.Status == "approved" || p.Status == "rejected" && recent) || !running && !recent && n >= 5 {
			continue
		}
		if failed {
			text, class = f.say, "rot"
		}
		n, busy, fresh = n+1, busy || running, fresh || recent
		// the card as it ends (#686): what the box does now, then what came of it; no decision buttons
		fmt.Fprintf(&list, "<div class=\"card %s\" data-f><b>#%d · %s</b><span class=state>%s</span>", class, p.ID, html.EscapeString(proposal.Card(p, domain).Summary), html.EscapeString(text))
		if p.Recomputed { // its branch no longer merged; laib applied its intent to the current main (#637)
			list.WriteString("<span class=note>neu berechnet auf dem aktuellen Stand</span>")
		}
		if done { // what was approved can be taken back (#41)
			list.WriteString(DecisionForm(p.ID, "rollback", "Zurücknehmen", ""))
		}
		if failed {
			list.WriteString(details(f.raw))
		}
		list.WriteString("</div>")
	}
	return list.String(), open, n, busy, fresh
}

// staleLines are the stale proposals still to act on (#716). One a later
// proposal on the same file or with the same reason answered is not shown:
// re-filed, decided or superseded, except the re-filed one still waiting.
// What git said stays behind the details.
func staleLines(all []*proposal.Proposal) string {
	var b strings.Builder
	for _, p := range all {
		if p.Status != "stale" || strings.HasPrefix(p.StaleReason, "ändert auf dem aktuellen Stand nichts mehr") {
			continue // "das steht schon so": nothing to do
		}
		if slices.ContainsFunc(all, func(q *proposal.Proposal) bool {
			same := q.Reason == p.Reason && p.Reason != "" || slices.ContainsFunc(q.Files, func(f string) bool { return slices.Contains(p.Files, f) })
			return q.ID > p.ID && same && !(q.ID == p.Refiled && q.Status == "open")
		}) {
			continue
		}
		if p.Refiled != 0 { // #637: the content changed since the approval
			fmt.Fprintf(&b, "<p class=gelb>#%d nicht angewendet: %s</p>", p.ID, html.EscapeString(p.StaleReason))
			continue
		}
		fmt.Fprintf(&b, "<p class=gelb>#%d ist veraltet: es passt nicht mehr zum aktuellen Stand — bitte neu vorschlagen.</p><details><summary>Details</summary><pre>%s</pre></details>", p.ID, html.EscapeString(p.StaleReason))
	}
	return b.String()
}

// progress is what an Inbox card says about a decision taken (#686): the
// tap on its way, the run's step while a run holds the lock, waiting, or
// how it ended. running: its block comes again, and no button shows.
func progress(p *proposal.Proposal, decision string, locked bool) (text, class string, running bool) {
	switch {
	case decision != "":
		return decision, "gelb", true
	case p.Status == "rejected": // #854: a no ends visibly, like a yes
		return "abgelehnt", "info", false
	case p.Step != "" && locked:
		return p.Step + " …", "gelb", true
	case p.Outcome == "waiting" && locked:
		return "wartet, bis die Box mit der Arbeit davor fertig ist", "gelb", true
	case p.Outcome == "waiting":
		return "fängt gleich an", "gelb", true
	case p.Outcome == "rolled-back" && p.Because == "": // taken back on request: done, not red
		return "zurückgenommen", "info", false
	case p.Outcome == "rolled-back" && strings.Contains(p.Because, "Journal"):
		return "zurückgenommen, " + p.Because, "rot", false
	case p.Outcome == "rolled-back": // and the next step (#854)
		return "zurückgenommen, " + p.Because + ". Die Box ist wieder wie vorher; was schiefging, steht im Journal.", "rot", false
	case p.Because != "": // a repair whose verb failed (#753)
		return p.Because, "rot", false
	}
	return "fertig", "grün", false
}

// card is one open proposal's decision card (#58, #616): headline and
// weight, what changes, what it means, who and why, the way back — the
// diff only behind the technical details.
func card(b *strings.Builder, r *http.Request, env *cli.Env, p *proposal.Proposal, domain string, box []string) {
	c := proposal.Card(p, domain, box...)
	fmt.Fprintf(b, "<section class=card data-f><h2>Vorschlag #%d · %s</h2>", p.ID, html.EscapeString(c.Summary))
	fmt.Fprintf(b, "<p><span class=\"risk %s\">%s</span> %s", c.Risk, c.Risk, html.EscapeString(c.RiskWhy))
	if c.Decider != "" {
		fmt.Fprintf(b, " · <strong>%s</strong>", html.EscapeString(c.Decider))
	}
	b.WriteString("</p>")
	if len(c.Changes) > 0 {
		b.WriteString("<h3>Was sich ändert</h3><ul class=changes>")
		for _, l := range c.Changes {
			op := l.Op
			if op == "=" || op == "" {
				op = ""
			} else {
				op = "<b>" + op + "</b> "
			}
			fmt.Fprintf(b, "<li>%s%s</li>", op, html.EscapeString(l.Text))
		}
		b.WriteString("</ul>")
	}
	b.WriteString("<h3>Was das für euch bedeutet</h3><ul>")
	for _, i := range c.Impact {
		cls := ""
		if strings.HasPrefix(i, "Achtung") {
			cls = " class=gelb"
		}
		fmt.Fprintf(b, "<li%s>%s</li>", cls, html.EscapeString(i))
	}
	b.WriteString("</ul>")
	fmt.Fprintf(b, "<h3>Wer will das und warum</h3><p>%s</p><h3>Rückgängig</h3><p>%s</p>", html.EscapeString(c.Why), html.EscapeString(c.Reversible))
	for _, w := range c.Warnings {
		fmt.Fprintf(b, "<p class=gelb>Hinweis: %s</p>", html.EscapeString(w))
	}
	approve := DecisionForm(p.ID, "approve", "Freigeben", "go")
	// a no asks for its reason (#544): the Operator learns from it; "Später" stays free
	reject := fmt.Sprintf("<form method=post action=/inbox/%d/reject onsubmit=\"const b=this.querySelector('button');b.disabled=true;b.textContent='wird abgelehnt …'\"><input name=grund maxlength=200 placeholder=\"Warum nicht? (kurz)\"> <button>Ablehnen</button></form>", p.ID)
	later := "<a href=\"/\"><button>Später</button></a>"
	if c.Risky {
		fmt.Fprintf(b, "<p>%s %s %s</p>", later, approve, reject)
	} else {
		fmt.Fprintf(b, "<p>%s %s %s</p>", approve, reject, later)
	}
	fmt.Fprintf(b, "<details><summary>Technische Details</summary><p class=muted>%s</p><pre>%s</pre></details>",
		html.EscapeString(c.Details), html.EscapeString(proposal.Diff(r.Context(), env.Paths.State, p)))
	b.WriteString("</section>")
}

// approvable are the open proposals a human on the box page may approve
// (#686): their tier within the human hand's ceiling. inbox.Decide checks
// each again when it is approved.
func approvable(env *cli.Env, open []*proposal.Proposal) []*proposal.Proposal {
	st, err := cli.LoadState(env)
	if err != nil {
		return nil
	}
	return slices.DeleteFunc(slices.Clone(open), func(p *proposal.Proposal) bool {
		return p.Status != "open" || DecisionState(p.ID) != "" || !policy.Allowed(st.Policy, "human", p.Tier)
	})
}

// oneByOne splits off what „Alle freigeben“ leaves for its own Freigeben
// (#799): what deletes (tier destroy, a removed service) and what the review
// flagged (class human, a warning). left says which and why, for the page.
func oneByOne(may []*proposal.Proposal) ([]*proposal.Proposal, string) {
	var b strings.Builder
	rest := slices.DeleteFunc(may, func(p *proposal.Proposal) bool {
		why := ""
		switch {
		case p.Tier == "destroy":
			why = "löscht etwas, das nicht mit einem Tipp zurückkommt"
		case p.Class == "remove":
			why = "entfernt einen Dienst"
		case p.Class == "human":
			why = "Laib will, dass du ihn dir selbst ansiehst"
		case len(p.Warnings) > 0:
			why = "hat einen Hinweis"
		default:
			return false
		}
		fmt.Fprintf(&b, "<li>#%d · %s: %s</li>", p.ID, html.EscapeString(proposal.Card(p, "").Summary), why)
		return true
	})
	if b.Len() == 0 {
		return rest, ""
	}
	return rest, "<h2>Diese gibst du einzeln frei</h2><p>Jeden mit seinem eigenen Knopf in der Inbox, dort siehst du, was er tut:</p><ul>" + b.String() + "</ul>"
}

// ApproveAll is „Alle freigeben“ (#686): GET is the one confirmation, the
// cards it covers by number; POST approves exactly those, one after the
// other, each with its own run — so a red one is taken back alone — and
// the Inbox shows each as it is processed. It sits behind the box page's
// second factor like Freigeben, and no token reaches it. What deletes or
// what the review flagged it leaves for its own Freigeben (#799).
func ApproveAll(env *cli.Env) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		open, _ := proposal.List(cli.InboxDir(env), true)
		may, left := oneByOne(approvable(env, open))
		if r.Method != http.MethodPost {
			if len(may) == 0 {
				page(w, "Inbox", "<p>Keine Vorschläge, die du gemeinsam freigeben kannst.</p>"+left+"<p><a href=/inbox>Inbox</a></p>")
				return
			}
			var b strings.Builder
			fmt.Fprintf(&b, "<h1>Alle freigeben</h1><p>Du gibst diese %d Vorschläge frei. Die Box wendet sie nacheinander an; geht einer schief, nimmt sie ihn zurück, die anderen bleiben.</p><form method=post action=/inbox/alle onsubmit=\"const b=this.querySelector('button');b.disabled=true;b.textContent='wird freigegeben …'\"><div class=liste>", len(may))
			for _, p := range may {
				c := proposal.Card(p, "")
				fmt.Fprintf(&b, "<div class=card><input type=hidden name=id value=%d><b>#%d · %s</b><span class=\"risk %s\">%s</span></div>", p.ID, p.ID, html.EscapeString(c.Summary), c.Risk, c.Risk)
			}
			fmt.Fprintf(&b, "</div><p><button class=go>Ja, alle %d freigeben</button> <a href=/inbox>Abbrechen</a></p></form>%s", len(may), left)
			page(w, "Inbox", b.String())
			return
		}
		r.ParseForm()
		var ids []int
		for _, p := range may { // exactly what the owner saw and still open
			if slices.Contains(r.Form["id"], strconv.Itoa(p.ID)) {
				ids = append(ids, p.ID)
				setDecision(p.ID, "kommt gleich dran")
			}
		}
		slices.Sort(ids)
		actor := webActor(r)
		go func() {
			for _, id := range ids {
				setDecision(id, "wird freigegeben …")
				decide(env, actor, id, "approve", []string{strconv.Itoa(id)}, 15*time.Minute)
			}
		}()
		http.Redirect(w, r, "/inbox", http.StatusSeeOther)
	}
}
