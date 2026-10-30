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

// Inbox is the Inbox page: the open decision cards oldest first, each
// decided one in its place with what the box does with it and what came
// of it (#686, #941), „Zuletzt entschieden“ at the bottom, what is stale.
func Inbox(env *cli.Env) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taps := decisionsNow() // before the list: a tap read as done is done in the list (#941)
		all, err := proposal.List(cli.InboxDir(env), false)
		var b strings.Builder
		b.WriteString("<h1>Inbox</h1>" + filter)
		if err != nil {
			b.WriteString(failure("Die Box kann die Vorschläge gerade nicht ganz lesen. Lade die Seite in einer Minute noch einmal.", err.Error()))
		}
		cards, _, bottom, open := placed(r, env, all, taps)
		if may, _ := oneByOne(approvable(env, open)); len(may) > 1 {
			fmt.Fprintf(&b, `<p><a href="/inbox/alle"><button class=go>Alle freigeben (%d)</button></a></p>`, len(may))
		}
		if cards == "" {
			b.WriteString("<p>Keine offenen Vorschläge.</p>")
		}
		b.WriteString(cards + bottom + staleLines(all))
		page(w, "Inbox", b.String())
	}
}

// InboxStand is what the live script swaps in (#686, #941): each decided
// card for its place, by id, and „Zuletzt entschieden“. The open cards
// are not in it, so a reason being typed is never touched.
func InboxStand(env *cli.Env) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taps := decisionsNow()
		all, _ := proposal.List(cli.InboxDir(env), false)
		_, live, bottom, _ := placed(r, env, all, taps)
		fragment(w, live+bottom)
	}
}

// placed puts every card in its place (#941), so nothing moves when one is
// decided: the open ones and those decided in the last 15 minutes by id,
// oldest first — a decided one as it ends, where it was (#686), a tap that
// failed with its reason above its buttons (#854) — and older decisions
// under „Zuletzt entschieden“, always at the bottom. live: the decided
// ones in their places, for the live script. taps: the taps on their way,
// read before all, so a tap done between the two reads still runs.
func placed(r *http.Request, env *cli.Env, all []*proposal.Proposal, taps map[int]string) (cards, live, bottom string, open []*proposal.Proposal) {
	domain, box := "", []string(nil)
	if st, err := cli.LoadState(env); err == nil {
		domain, box = st.Box.Domain, st.Warnings
	}
	dir, locked := cli.InboxDir(env), apply.LockHolder(env.Paths) != ""
	all = slices.Clone(all)
	slices.SortFunc(all, func(a, b *proposal.Proposal) int { return a.ID - b.ID })
	var top, ended strings.Builder
	type past struct {
		at   time.Time
		html string
	}
	var older []past
	for _, p := range all {
		decision := taps[p.ID]
		f, failed := failedTap(dir, p.ID)
		failed = failed && decision == ""
		if p.Status == "open" && decision == "" {
			open = append(open, p)
			red := ""
			if failed {
				red = failure(f.say, f.raw)
			}
			c := card(r, env, p, domain, box, red)
			top.WriteString(c)
			if failed {
				ended.WriteString(c)
			}
			continue
		}
		text, class, running := progress(p, decision, locked)
		recent := failed || time.Since(p.Decided) < 15*time.Minute
		here := p.Status == "open" || time.Since(p.Decided) < 15*time.Minute
		if !(running || failed || p.Status == "approved" || p.Status == "rejected" && recent) {
			continue
		}
		if failed {
			text, class = f.say, "rot"
		}
		if here {
			c := decidedCard(p, all, domain, text, class, running, failed, f.raw, true)
			top.WriteString(c)
			ended.WriteString(c)
			continue
		}
		older = append(older, past{p.Decided, decidedCard(p, all, domain, text, class, running, failed, f.raw, false)})
	}
	// „Zuletzt entschieden“: the five decided last, the newest at the top (owner, 2026-10-30: it
	// listed #1–#5 from migration day, the cards sorted by id for their places above)
	slices.SortStableFunc(older, func(a, b past) int { return b.at.Compare(a.at) })
	bottom = "<div id=laeuft></div>"
	if len(older) > 0 {
		var list strings.Builder
		for _, o := range older[:min(5, len(older))] {
			list.WriteString(o.html)
		}
		bottom = "<div id=laeuft><h2>Zuletzt entschieden</h2><div class=liste>" + list.String() + "</div></div>"
	}
	return top.String(), ended.String(), bottom, open
}

// decidedCard is a card as it ends (#686): what the box does now, then
// what came of it; no decision buttons. here: in its place, with its
// headline as an open card has it; else one line under „Zuletzt
// entschieden“. data-run while it runs, with its limit (#854).
func decidedCard(p *proposal.Proposal, all []*proposal.Proposal, domain, text, class string, running, failed bool, raw string, here bool) string {
	var b strings.Builder
	run := ""
	if running {
		run = " data-run"
	}
	summary := html.EscapeString(proposal.Card(p, domain).Summary)
	if here {
		fmt.Fprintf(&b, "<section class=\"card %s\" id=%d data-f%s><h2>Vorschlag #%d · %s</h2><p class=state>%s</p>", class, p.ID, run, p.ID, summary, html.EscapeString(text))
	} else {
		fmt.Fprintf(&b, "<div class=\"card %s\" id=%d data-f%s><b>#%d · %s</b><span class=state>%s</span>", class, p.ID, run, p.ID, summary, html.EscapeString(text))
	}
	if running {
		b.WriteString(frist("Meist ist sie in ein, zwei Minuten fertig, mit Arbeit davor auch länger."))
	}
	if p.Recomputed { // its branch no longer merged; laib applied its intent to the current main (#637)
		b.WriteString("<span class=note>neu berechnet auf dem aktuellen Stand</span>")
	}
	if undo, _ := proposal.Undoing(p); text == "fertig" && undo { // what was approved can be taken back (#41), where that undoes something (#852)
		b.WriteString(DecisionForm(p.ID, "rollback", "Zurücknehmen", ""))
	}
	for _, way := range []struct { // a red run with data (#853): its two ways on, laib's proposals, while they are open
		id    int
		label string
	}{{p.Restore, "Sicherung zurückspielen"}, {p.Forward, "Nach vorne reparieren"}} {
		if way.id != 0 && slices.ContainsFunc(all, func(q *proposal.Proposal) bool {
			return q.ID == way.id && q.Status == "open" && DecisionState(q.ID) == ""
		}) {
			b.WriteString(DecisionForm(way.id, "approve", way.label, ""))
		}
	}
	if failed {
		b.WriteString(details(raw))
	}
	if here {
		return b.String() + "</section>"
	}
	return b.String() + "</div>"
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
// diff only behind the technical details. red: a tap on it that failed,
// above everything else. One button row on every card (#941): Freigeben
// left, Ablehnen right; a no takes a second tap, with its reason.
func card(r *http.Request, env *cli.Env, p *proposal.Proposal, domain string, box []string, red string) string {
	c := proposal.Card(p, domain, box...)
	b := &strings.Builder{}
	fmt.Fprintf(b, "<section class=card id=%d data-f><h2>Vorschlag #%d · %s</h2>%s", p.ID, p.ID, html.EscapeString(c.Summary), red) // /inbox#N, where a message links (#857)
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
	fmt.Fprintf(b, "<h3>Wer will das und warum</h3><p>%s</p><h3>Rückgängig</h3><p>%s</p>", strings.ReplaceAll(html.EscapeString(c.Why), "\n", "<br>"), html.EscapeString(c.Reversible))
	for _, w := range c.Warnings {
		fmt.Fprintf(b, "<p class=gelb>Hinweis: %s</p>", html.EscapeString(w))
	}
	accent := "go"
	if c.Risky { // „Freigeben“ does not read as the default (#852); the risk stands above the row
		accent = ""
		fmt.Fprintf(b, "<p class=risiko>Vorsicht: %s Lies oben, was sich ändert, bevor du freigibst.</p>", html.EscapeString(c.RiskWhy))
	}
	// a no asks for its reason (#544), optional, only after the first tap (#941)
	fmt.Fprintf(b, `<div class=knoepfe>%s<button type=button aria-expanded=false onclick="const f=this.parentNode.nextElementSibling;f.hidden=false;this.setAttribute('aria-expanded','true');f.querySelector('input').focus()">Ablehnen</button></div>`, DecisionForm(p.ID, "approve", "Freigeben", accent))
	fmt.Fprintf(b, `<form class=nein method=post action=/inbox/%d/reject hidden onsubmit="const b=this.querySelector('button');b.disabled=true;b.textContent='wird abgelehnt …'"><input name=grund maxlength=200 placeholder="Warum nicht? (optional)" aria-label="Warum nicht? (optional)"><span class=knoepfe><button>Ablehnen bestätigen</button><button type=button onclick="this.form.hidden=true;this.form.previousElementSibling.lastChild.setAttribute('aria-expanded','false')">Abbrechen</button></span></form>`, p.ID)
	fmt.Fprintf(b, "<details><summary>Technische Details</summary><p class=muted>%s</p><pre>%s</pre></details>",
		html.EscapeString(c.Details), html.EscapeString(proposal.Diff(r.Context(), env.Paths.State, p)))
	return b.String() + "</section>"
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
