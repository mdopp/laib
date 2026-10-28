package pages

import (
	"cmp"
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// Serve is the daemon: /ready for Solaris and the pages, and the three
// pages (Status, Inbox, Journal) as plain server-rendered HTML. Caddy and
// Authelia sit in front of it; the listener is loopback only.
func Serve(ctx context.Context, env *cli.Env, args []string) int {
	addr := "127.0.0.1:5900"
	if st, err := cli.LoadState(env); err == nil {
		addr = fmt.Sprintf("%s:%d", st.Box.Laib.Listen, st.Box.Laib.Port)
	}
	if v := os.Getenv("LAIB_LISTEN"); v != "" {
		addr = v
	}
	// viaProxy: the pages trust Remote-User only behind Caddy's secret header.
	// Before a proxy role exists there is no secret and loopback is the only
	// listener, so the pages are open to the box operator (ADR 0012).
	viaProxy := func(r *http.Request) bool {
		sec, err := secrets.Load(env.Paths.Secrets, env.Paths.Key)
		if err != nil {
			return false
		}
		want, ok := sec[render.ProxySecret]
		if !ok || want == "" {
			return strings.HasPrefix(r.RemoteAddr, "127.0.0.1:")
		}
		return subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Laib-Proxy")), []byte(want)) == 1
	}
	guard := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !viaProxy(r) {
				http.Error(w, "nur über die Box-Seite (Caddy und Authelia)", http.StatusForbidden)
				return
			}
			h(w, r)
		}
	}
	// boot-time convergence of the non-file state (#37): the ruleset
	if loaded, err := apply.EnsureFirewall(ctx, env.Paths); err != nil {
		fmt.Fprintln(env.Errw(), "laib serve: firewall:", err)
	} else if loaded {
		if st, err := cli.LoadState(env); err == nil {
			cli.Record(env, st, journal.Entry{Actor: "laib", Verb: "serve", Result: "info", Detail: "firewall: rendered ruleset loaded into the kernel (boot)"})
		}
	}
	go inbox.Carry(context.Background(), &cli.Env{Paths: env.Paths, Actor: "laib"}) // a run a restart cut off before it applied what waits (#609)
	mux := Routes(env, guard)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return env.Fail(err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	// Boot comparison (#9): bereit only once units and files match the state.
	if st, err := cli.LoadState(env); err == nil {
		r, _ := CollectStatus(ctx, env, st)
		if r.Red > 0 || r.Drift > 0 {
			BootUnclean = fmt.Sprintf("Nach dem Start ist die Box nicht ganz wie eingestellt: %d Dienste rot, %d Dateien weichen ab. Laib gleicht das beim nächsten Aufräumen an; sieh auf der Status-Seite nach.", r.Red, r.Drift)
			cli.Record(env, st, journal.Entry{Actor: "laib", Verb: "ready", Result: "red", Detail: BootUnclean})
		} else {
			cli.Record(env, st, journal.Entry{Actor: "laib", Verb: "ready", Result: "green", Detail: fmt.Sprintf("%d Dienste grün", r.Green)})
		}
	}
	journal.Append(env.Paths.Journal, journal.Entry{Actor: "laib", Verb: "serve", Result: "info", Detail: "listening on " + addr})
	fmt.Fprintln(env.Errw(), "laib serve: listening on", addr)
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return env.Fail(err)
	}
	return cli.ExitOK
}

// Routes are the daemon's routes: /mcp, /ready and the pages, each page
// behind guard (Caddy's secret header in Serve).
func Routes(env *cli.Env, guard func(http.HandlerFunc) http.HandlerFunc) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", mcp.Handler(env))
	mux.HandleFunc("/v1/", FleetLock(env, time.Now))
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		st, err := cli.LoadState(env)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"state": "eingeschränkt", "lock": proposal.NotReadable})
			return
		}
		rd, _ := CollectStatus(r.Context(), env, st)
		json.NewEncoder(w).Encode(rd)
	})
	mux.HandleFunc("/", guard(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		st, err := cli.LoadState(env)
		if err != nil {
			page(w, "Status", failure(proposal.NotReadable, err.Error()))
			return
		}
		rd, rows := CollectStatus(r.Context(), env, st)
		checking := checks(r.Context(), st, true)
		last := ""
		if e, ok := journal.Last(env.Paths.Journal); ok {
			last = fmt.Sprintf("%s · %s · %s %s · %s", e.T.Local().Format("02.01. 15:04"), e.Actor, e.Verb, e.Service, e.Reason)
		}
		body := statusBody(rd, rows, checking, last, r.URL.Query().Get("filter"))
		for _, w := range st.Warnings { // the box's own warnings, once here, not on every proposal (#616)
			body += "<p class=gelb>Hinweis: " + html.EscapeString(w) + "</p>"
		}
		page(w, "Status", body)
	}))
	// the Status page's cards for the live script, without starting a check
	mux.HandleFunc("GET /stand", guard(func(w http.ResponseWriter, r *http.Request) {
		st, err := cli.LoadState(env)
		if err != nil {
			http.Error(w, proposal.NotReadable, http.StatusServiceUnavailable)
			return
		}
		_, rows := CollectStatus(r.Context(), env, st)
		checking, cards := checks(r.Context(), st, false), ""
		for _, row := range rows {
			cards += serviceCard(row, checking)
		}
		fragment(w, cards)
	}))
	mux.HandleFunc("/abmelden", Abmelden(env))
	mux.HandleFunc("/inbox", guard(Inbox(env)))
	mux.HandleFunc("/inbox/alle", guard(ApproveAll(env)))
	mux.HandleFunc("GET /inbox/stand", guard(InboxStand(env)))
	mux.HandleFunc("/schluessel", guard(Keys(env))) // a key from outside, by the owner (#696)
	mux.HandleFunc("GET /schluessel/stand", guard(KeysStand))
	mux.HandleFunc("POST /inbox/{id}/{decision}", guard(Decision(env)))
	// "jetzt prüfen" (#309): the service's check, started without waiting
	mux.HandleFunc("POST /check/{service}", guard(func(w http.ResponseWriter, r *http.Request) {
		st, err := cli.LoadState(env)
		if err != nil {
			http.Error(w, proposal.NotReadable, http.StatusServiceUnavailable)
			return
		}
		svc, ok := st.Services[r.PathValue("service")]
		if !ok || !diag.Refresh(r.Context(), svc) {
			w.WriteHeader(http.StatusNotFound)
			page(w, "Status", "<p class=rot>Für diesen Dienst gibt es keine Prüfung. <a href=/>Zurück zur Status-Seite</a></p>")
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}))
	mux.HandleFunc("/journal", guard(func(w http.ResponseWriter, r *http.Request) {
		es, err := journal.Read(env.Paths.Journal, time.Now().Add(-7*24*time.Hour))
		var b strings.Builder
		b.WriteString("<h1>Journal · 7 Tage</h1>" + filter)
		if err != nil {
			b.WriteString(failure("Das Journal lässt sich gerade nicht ganz lesen. Lade die Seite in einer Minute noch einmal.", err.Error()))
		}
		b.WriteString(journalBody(es))
		page(w, "Journal", b.String())
	}))
	return mux
}

// checks: per service with a status check, whether it runs now. refresh
// starts a stale one first (#309): the page does, its fragment does not.
func checks(ctx context.Context, st *state.State, refresh bool) map[string]bool {
	checking := map[string]bool{}
	for _, name := range st.ServiceNames() {
		svc := st.Services[name]
		if svc.Contract.Status == nil || svc.Contract.Status.Check == "" {
			continue
		}
		if refresh && diag.Stale(svc, diag.ReadStatus(st, svc), time.Now()) {
			checking[name] = diag.Refresh(ctx, svc)
		} else {
			checking[name] = sysd.Active(ctx, svc.Contract.Status.Check) == "activating"
		}
	}
	return checking
}

// live keeps what runs current without reloading the page, since the
// owner copies from it (2026-10-27): while the page holds an element
// marked data-run, it fetches <path>/stand every 3 s and swaps in the
// answer's top elements by id, never one holding the focus or a
// selection (that one comes next time), nothing else; it stops once
// nothing runs. It never stops silently (#854): a non-OK answer, five
// failed polls in a row or a wait past liveLimit say so under the nav,
// with the time of the last state. Without JS, the link.
const live = `<noscript><p><a href="">aktualisieren</a></p></noscript><script>(()=>{let n=0,s=new Date();const a=Date.now(),h=()=>s.toTimeString().slice(0,5),say=m=>document.querySelector("nav").insertAdjacentHTML("afterend","<p class=rot>"+m+"</p>"),off=()=>say("Keine Verbindung zur Box – Stand von "+h()+". Lade die Seite neu, sobald die Box wieder erreichbar ist."),t=()=>setTimeout(async()=>{if(Date.now()-a>` + liveLimit + `)return say("Das dauert länger als gedacht – Stand von "+h()+". Lade die Seite neu oder sieh im Journal nach.");try{const r=await fetch(location.pathname.replace(/\/$/,"")+"/stand",{cache:"no-store"});if(!r.ok)return off();const d=new DOMParser().parseFromString(await r.text(),"text/html"),g=getSelection();let run=d.querySelector("[data-run]");d.body.querySelectorAll(":scope>[id]").forEach(n=>{const o=document.getElementById(n.id);if(!o)return;if(o.contains(document.activeElement)||!g.isCollapsed&&g.getRangeAt(0).intersectsNode(o))run=1;else o.replaceWith(n)});document.querySelector(".filter")?.dispatchEvent(new Event("input"));s=new Date();n=0;if(!run)return}catch(e){if(++n>4)return off()}t()},3000);t()})()</script>`

// liveLimit is how long the live script waits, in ms: 20 minutes, what
// frist tells the person.
const liveLimit = "1200000"

// frist is the limit a wait states (#854): how long it usually takes, and
// that the page stops waiting after liveLimit and says so.
func frist(usually string) string {
	return "<small class=frist>" + usually + " Du musst nichts tun, die Seite aktualisiert sich; nach 20 Minuten hört sie auf zu warten und sagt es dir.</small>"
}

// failure is a red line in words with the raw text under Details (#855).
func failure(say, raw string) string {
	return "<p class=rot>" + html.EscapeString(say) + "</p>" + details(raw)
}

// details is the raw text, for the admin, behind a tap (#855).
func details(raw string) string {
	if raw == "" {
		return ""
	}
	return "<details><summary>Details</summary><pre>" + html.EscapeString(raw) + "</pre></details>"
}

// fragment answers the live script: the blocks alone, never cached.
func fragment(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, body)
}

func resultClass(r string) string {
	switch r {
	case "green":
		return "grün"
	case "red":
		return "rot"
	}
	return "info"
}

// Decision is the Inbox's POST (#574). A proposal that is no longer open
// is answered with what is true — a second submit of an approved one is not
// a failure — and a real failure shows its error line, not an exit code.
func Decision(env *cli.Env) http.HandlerFunc {
	done := map[string]string{"approved": "freigegeben", "rejected": "abgelehnt", "stale": "veraltet"}
	return func(w http.ResponseWriter, r *http.Request) {
		id, decision := r.PathValue("id"), r.PathValue("decision")
		n, err := strconv.Atoi(id)
		if err != nil {
			page(w, "Inbox", "<p class=rot>Entscheidung fehlgeschlagen: „"+html.EscapeString(id)+"“ ist keine Nummer eines Vorschlags. Zurück zur <a href=/inbox>Inbox</a>.</p>")
			return
		}
		if p, err := proposal.Get(cli.InboxDir(env), n); err != nil {
			page(w, "Inbox", failure(fmt.Sprintf("Entscheidung fehlgeschlagen: Vorschlag #%d gibt es nicht (mehr). Lade die Inbox neu.", n), err.Error())+"<p><a href=/inbox>Inbox</a> · <a href=/journal>Journal</a></p>")
			return
		} else if decision != "rollback" && p.Status != "open" {
			page(w, "Inbox", fmt.Sprintf("<p>#%d ist schon %s, von %s, um %s.</p><p><a href=/inbox>Inbox</a> · <a href=/journal>Journal</a></p>",
				p.ID, done[p.Status], html.EscapeString(strings.TrimPrefix(p.DecidedBy, "human:")), p.Decided.Local().Format("15:04")))
			return
		}
		argv := []string{id}
		if why := strings.TrimSpace(r.FormValue("grund")); why != "" && decision == "reject" {
			argv = append(argv, why)
		}
		setFailed(cli.InboxDir(env), n, "", "")
		setDecision(n, cmp.Or(map[string]string{"approve": "wird freigegeben …", "reject": "wird abgelehnt …"}[decision], "wird zurückgenommen …"))
		go decide(env, webActor(r), n, decision, argv, 15*time.Minute) // like a human at the shell (#753)
		http.Redirect(w, r, "/inbox", http.StatusSeeOther)
	}
}

// decisions are the taps not yet done (#607), each carried out on its own,
// detached from the request that took it: a closed connection cuts no
// apply off. An approve is a merge and done at once (#609); the run that
// applies it says itself, on the proposal, that it waits.
var decisions = struct {
	sync.Mutex
	state map[int]string
}{state: map[int]string{}}

// DecisionState is what the Inbox says about a decision taken and not yet
// done, or why it failed. Empty when done.
func DecisionState(id int) string {
	decisions.Lock()
	defer decisions.Unlock()
	return decisions.state[id]
}

func setDecision(id int, s string) {
	decisions.Lock()
	defer decisions.Unlock()
	if s == "" {
		delete(decisions.state, id)
	} else {
		decisions.state[id] = s
	}
}

// failures are the taps that ended red (#607, #854), per inbox: the Inbox
// shows each where its card was for 15 minutes, in words (#855), the raw
// line under Details and in the journal.
var failures = struct {
	sync.Mutex
	m map[string]failed
}{m: map[string]failed{}}

type failed struct {
	say, raw string
	at       time.Time
}

func setFailed(dir string, id int, say, raw string) {
	failures.Lock()
	defer failures.Unlock()
	if say == "" {
		delete(failures.m, dir+"#"+strconv.Itoa(id))
	} else {
		failures.m[dir+"#"+strconv.Itoa(id)] = failed{say, raw, time.Now()}
	}
}

func failedTap(dir string, id int) (failed, bool) {
	failures.Lock()
	defer failures.Unlock()
	f, ok := failures.m[dir+"#"+strconv.Itoa(id)]
	return f, ok && time.Since(f.at) < 15*time.Minute
}

// wait: how long an approve's run waits for the lock (Alle freigeben, #686).
func decide(env *cli.Env, actor string, id int, decision string, argv []string, wait time.Duration) {
	var errOut strings.Builder
	ctx, webEnv := context.Background(), &cli.Env{Paths: env.Paths, Actor: actor, Out: io.Discard, ErrOut: &errOut, Wait: wait}
	var code int
	if decision == "rollback" {
		code = inbox.Rollback(ctx, webEnv, []string{"#" + strconv.Itoa(id)})
	} else if code = inbox.Decide(ctx, webEnv, argv, decision == "approve"); code == cli.ExitOK && decision == "approve" {
		setDecision(id, "") // approved: from here the proposal says it waits for its run
		code = inbox.Carry(ctx, webEnv)
	}
	if code != cli.ExitOK && code != cli.ExitRed { // first the failure, then the tap is no longer on its way
		lines := strings.Split(strings.TrimSpace(errOut.String()), "\n")
		raw := strings.TrimPrefix(lines[len(lines)-1], "laib: ")
		setFailed(cli.InboxDir(env), id, "fehlgeschlagen: "+proposal.Plain(raw), raw)
		journal.Append(env.Paths.Journal, journal.Entry{Actor: actor, Verb: decision, Result: "red", Detail: fmt.Sprintf("#%d auf der Box-Seite: %s (Code %d)", id, raw, code)})
	}
	setDecision(id, "")
}

// webActor is who decides on the box page: Authelia hands the user in
// Remote-User; without it the actor is the web itself.
func webActor(r *http.Request) string {
	return "human:" + cmp.Or(r.Header.Get("Remote-User"), "web")
}

// DecisionForm is one Inbox button (#574): on submit it disables itself and
// says it is working, so nobody taps twice while the apply runs.
func DecisionForm(id int, action, label, class string) string {
	return fmt.Sprintf(`<form method=post action=/inbox/%d/%s onsubmit="const b=this.querySelector('button');b.disabled=true;b.textContent='wird angewendet …'"><button class="%s">%s</button></form>`, id, action, class, label)
}

// Abmelden is the way out (#480): Authelia's logout, then the login for the
// box page again, so another account can sign in.
func Abmelden(env *cli.Env) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st, err := cli.LoadState(env)
		if err != nil {
			http.Error(w, proposal.NotReadable, http.StatusServiceUnavailable)
			return
		}
		back := "https://" + st.Box.Laib.Host + "." + st.Box.Domain + "/"
		http.Redirect(w, r, render.LogoutURL(st)+"?rd="+url.QueryEscape(back), http.StatusSeeOther)
	}
}

// tabs are the pages in the nav (#515, haus#67): name, where, icon (24×24, stroked).
var tabs = []struct{ name, href, icon string }{
	{"Status", "/", `<rect x="3" y="7" width="18" height="12" rx="2"/><path d="M3 11h18M8 15h2"/>`},
	{"Inbox", "/inbox", `<path d="M4 13l2.5-8h11l2.5 8v6H4z"/><path d="M4 13h5l1 2h4l1-2h5"/>`},
	{"Journal", "/journal", `<path d="M6 4h12v16H6z"/><path d="M9 9h6M9 13h6M9 17h3"/>`},
	{"Abmelden", "/abmelden", `<path d="M14 4h5v16h-5M10 8l-4 4 4 4M6 12h10"/>`},
}

// navBar is the pages' nav (#515): a bar at the bottom on the phone, a
// list on the right on a wide screen (laib.css), the current page marked.
func navBar(current string) string {
	var b strings.Builder
	b.WriteString("<nav class=tabs aria-label=Seiten>")
	for _, t := range tabs {
		b.WriteString(`<a href="` + t.href + `"`)
		if t.name == current {
			b.WriteString(" aria-current=page")
		}
		b.WriteString(`><svg viewBox="0 0 24 24" aria-hidden=true>` + t.icon + "</svg>" + t.name + "</a>")
	}
	return b.String() + "</nav>"
}

// filter narrows a list page to the cards (data-f) whose text holds what is
// typed, without a reload, case-insensitive (#515).
const filter = `<input type=search class=filter placeholder="Filtern, z. B. lan, immich, 8083" aria-label=Filtern oninput="const q=this.value.toLowerCase();document.querySelectorAll('[data-f]').forEach(e=>e.hidden=!e.textContent.toLowerCase().includes(q))">`

//go:embed laib.css
var css string

func page(w http.ResponseWriter, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, pageHTML(title, body))
}

// pageHTML is a page in the household look (#382): the CSS inline from the
// one embedded file, no request beyond the page itself.
func pageHTML(title, body string) string {
	return `<!doctype html><html lang=de><head><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1"><meta name=color-scheme content="dark light"><meta name=theme-color content="#0c0c0e"><title>Laib · ` +
		html.EscapeString(title) + "</title><style>\n" + css + "</style></head><body>" + navBar(title) + body + liveIf(body) + "</body></html>\n"
}

// liveIf is the live script on a page where something runs; escaped text
// never holds the marker's ">".
func liveIf(body string) string {
	if strings.Contains(body, " data-run>") {
		return live
	}
	return ""
}

// statusBody is the status page: the box's line, one card per service with
// its state and note, the last change; the morning report is Haus's (#798).
func statusBody(rd Readiness, rows []ServiceStatus, checking map[string]bool, last string, show string) string {
	var b strings.Builder
	// the owner's filter by state (#785): problems first; a paused service counts green
	group := func(s string) string {
		if s == "rot" || s == "gelb" {
			return s
		}
		return "gruen"
	}
	rows = slices.Clone(rows)
	slices.SortStableFunc(rows, func(a, c ServiceStatus) int {
		return strings.Index("rot gelb gruen", group(a.State)) - strings.Index("rot gelb gruen", group(c.State))
	})
	count := map[string]int{"": len(rows)}
	for _, row := range rows {
		count[group(row.State)]++
	}
	if !slices.Contains([]string{"rot", "gelb", "gruen"}, show) {
		show = ""
	}
	heard := rd.LastHeard
	if len(heard) >= 19 {
		heard = heard[11:19]
	}
	fmt.Fprintf(&b, "<h1>Laib · %s · %d Dienste</h1><p class=muted>Operator %s · zuletzt gehört %s</p>", html.EscapeString(rd.State), rd.Services, html.EscapeString(rd.Operator), heard)
	b.WriteString("<p class=stand>")
	for i, f := range [][2]string{{"", "Alle"}, {"rot", "Rot"}, {"gelb", "Gelb"}, {"gruen", "Grün"}} {
		href, cur := "/", ""
		if i > 0 {
			b.WriteString(" · ")
		}
		if f[0] != "" {
			href += "?filter=" + f[0]
		}
		if f[0] == show {
			cur = " aria-current=page"
		}
		fmt.Fprintf(&b, `<a href="%s"%s>%s %d</a>`, href, cur, f[1], count[f[0]])
	}
	b.WriteString("</p>")
	b.WriteString(`<p><a href="/schluessel">Schlüssel hinterlegen</a></p>`)
	if rd.Lock != "" && strings.Contains(rd.Lock, " pid ") { // a run holds the lock: in words, who under Details (#855)
		b.WriteString("<p class=gelb>Die Box arbeitet gerade an einer Änderung; das dauert meist ein paar Minuten.</p>" + details("gesperrt durch "+rd.Lock))
	} else if rd.Lock != "" {
		fmt.Fprintf(&b, "<p class=gelb>%s</p>", html.EscapeString(rd.Lock))
	}
	b.WriteString(operatorSection(rd.OperatorWork) + "<h2>Dienste</h2>")
	b.WriteString(filter + "<div class=liste>")
	for _, row := range rows {
		if show != "" && group(row.State) != show {
			continue
		}
		b.WriteString(serviceCard(row, checking))
	}
	b.WriteString("</div>")
	if last != "" {
		fmt.Fprintf(&b, "<p class=muted>Letzte Änderung %s</p>", html.EscapeString(last))
	}
	return b.String()
}

// journalBody is the journal's entries, newest first, one row card each.
// serviceCard is one service on the Status page, by id for the live
// script; data-run while its check runs.
func serviceCard(row ServiceStatus, checking map[string]bool) string {
	var bad []string
	for u, a := range row.Units {
		if a != "active" {
			bad = append(bad, u+"="+a)
		}
	}
	sort.Strings(bad)
	note, units := row.Detail, "" // which unit is how: under Details (#855)
	if len(bad) > 0 {
		note = strings.TrimSuffix(map[bool]string{true: "startet gerade", false: "läuft nicht; sieh in der Inbox nach, ob eine Reparatur wartet"}[row.State == "gelb"]+" · "+note, " · ")
		units = details(strings.Join(bad, " "))
	}
	check, run := "", ""
	if on, ok := checking[row.Name]; ok {
		check = `<form method=post action="/check/` + html.EscapeString(row.Name) + `"><button>jetzt prüfen</button></form>`
		if on {
			check, run = "<span class=muted>wird geprüft …</span>"+frist("Meist ist die Prüfung in einer Minute fertig."), " data-run"
		}
	}
	name := "<b>" + html.EscapeString(row.Name) + "</b>"
	if row.Title != "" { // the plain name big, what it is beside it, the id small (#785)
		name = "<b>" + html.EscapeString(row.Title) + "</b><span class=muted>" + html.EscapeString(row.Beschreibung) + "</span><small class=muted>" + html.EscapeString(row.Name) + "</small>"
	}
	return fmt.Sprintf("<div class=\"card %s\" id=\"svc-%s\" data-f%s>%s<span class=state>%s</span><span class=note>%s</span>%s%s</div>\n", row.State, html.EscapeString(row.Name), run, name, row.State, html.EscapeString(note), check, units)
}

func journalBody(es []journal.Entry) string {
	var b strings.Builder
	b.WriteString("<div class=liste>")
	for i := len(es) - 1; i >= 0; i-- {
		e := es[i]
		fmt.Fprintf(&b, "<div class=\"card %s\" data-f><span class=muted>%s</span><b>%s %s</b><span class=state>%s</span><span class=note>%s · %s %s</span></div>\n", resultClass(e.Result), e.T.Local().Format("02.01. 15:04:05"), e.Verb, html.EscapeString(e.Service), e.Result, html.EscapeString(e.Actor), html.EscapeString(e.Reason), html.EscapeString(e.Detail))
	}
	return b.String() + "</div>"
}
