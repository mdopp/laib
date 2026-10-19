package pages

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
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
				http.Error(w, "nur über laib.<domain> (Authelia)", http.StatusForbidden)
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
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", mcp.Handler(env))
	mux.HandleFunc("/v1/", FleetLock(env, time.Now))
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		st, err := cli.LoadState(env)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"state": "eingeschränkt", "lock": "state: " + err.Error()})
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
			page(w, "Status", "<p class=rot>State nicht lesbar: "+html.EscapeString(err.Error())+"</p>")
			return
		}
		rd, rows := CollectStatus(r.Context(), env, st)
		// a status the admin looks at is checked again when it is stale, in
		// the background (#309); while a check runs the page reloads itself
		checking := map[string]bool{}
		for _, name := range st.ServiceNames() {
			svc := st.Services[name]
			if svc.Contract.Status == nil || svc.Contract.Status.Check == "" {
				continue
			}
			if diag.Stale(svc, diag.ReadStatus(st, svc), time.Now()) {
				checking[name] = diag.Refresh(r.Context(), svc)
			} else {
				checking[name] = sysd.Active(r.Context(), svc.Contract.Status.Check) == "activating"
			}
		}
		last := ""
		if e, ok := journal.Last(env.Paths.Journal); ok {
			last = fmt.Sprintf("%s · %s · %s %s · %s", e.T.Local().Format("02.01. 15:04"), e.Actor, e.Verb, e.Service, e.Reason)
		}
		day, text := LastReport(env)
		page(w, "Status", statusBody(rd, rows, checking, last, [2]string{day, text}, BoteSignalBase(st) != ""))
	}))
	// how the Bote gets its Signal number (#469): registered or linked, with signal-cli once
	mux.HandleFunc("/bote/link", guard(func(w http.ResponseWriter, r *http.Request) {
		st, err := cli.LoadState(env)
		if err != nil || BoteSignalBase(st) == "" {
			http.Error(w, "kein Bote mit Signal auf dieser Box", http.StatusNotFound)
			return
		}
		page(w, "Bote verbinden", "<h1>Bote verbinden</h1><p>Der Bote braucht eine eigene Signal-Nummer, einmal auf der Box eingerichtet:</p><pre>"+BoteLinkPage(st.Box.DataDir, boteAccount(st))+"</pre><p><a href=\"/\">zurück</a></p>")
	}))
	mux.HandleFunc("/inbox", guard(func(w http.ResponseWriter, r *http.Request) {
		open, err := proposal.List(cli.InboxDir(env), true)
		var b strings.Builder
		b.WriteString(nav + "<h1>Inbox</h1>")
		if err != nil {
			b.WriteString("<p class=rot>" + html.EscapeString(err.Error()) + "</p>")
		}
		if len(open) == 0 {
			b.WriteString("<p>Keine offenen Vorschläge.</p>")
		}
		domain := ""
		if st, err := cli.LoadState(env); err == nil {
			domain = st.Box.Domain
		}
		for _, p := range open {
			// the decision card (#58): consequence, reversibility, who and why,
			// a safe default — the diff only behind Details
			c := proposal.Card(p, domain)
			fmt.Fprintf(&b, "<section class=card><h2>Vorschlag #%d · %s</h2>", p.ID, html.EscapeString(c.Summary))
			b.WriteString("<ul>")
			for _, i := range c.Impact {
				cls := ""
				if strings.HasPrefix(i, "Achtung") {
					cls = " class=gelb"
				}
				fmt.Fprintf(&b, "<li%s>%s</li>", cls, html.EscapeString(i))
			}
			b.WriteString("</ul>")
			fmt.Fprintf(&b, "<p>%s</p><p>%s</p>", html.EscapeString(c.Reversible), html.EscapeString(c.Why))
			if p.Class == "human" {
				b.WriteString("<p class=rot>Nur ein Mensch kann freigeben.</p>")
			}
			for _, w := range p.Warnings {
				fmt.Fprintf(&b, "<p class=gelb>Hinweis: %s</p>", html.EscapeString(w))
			}
			approve := fmt.Sprintf("<form method=post action=/inbox/%d/approve><button class=go>Freigeben</button></form>", p.ID)
			reject := fmt.Sprintf("<form method=post action=/inbox/%d/reject><button>Ablehnen</button></form>", p.ID)
			later := "<a href=\"/\"><button>Später</button></a>"
			if c.Risky {
				fmt.Fprintf(&b, "<p>%s %s %s</p>", later, approve, reject)
			} else {
				fmt.Fprintf(&b, "<p>%s %s %s</p>", approve, reject, later)
			}
			fmt.Fprintf(&b, "<details><summary>Details (von %s, Stufe %s, braucht eine andere Hand mit %s)</summary><pre>%s</pre></details>",
				html.EscapeString(p.Actor), p.Tier, p.Tier, html.EscapeString(proposal.Diff(r.Context(), env.Paths.State, p)))
			b.WriteString("</section>")
		}
		// what was approved lately can be taken back (#41)
		if all, err := proposal.List(cli.InboxDir(env), false); err == nil {
			n := 0
			for _, p := range all {
				if p.Status != "approved" || n >= 5 {
					continue
				}
				if n == 0 {
					b.WriteString("<h2>Zuletzt freigegeben</h2>")
				}
				n++
				fmt.Fprintf(&b, "<div class=card><span class=note>#%d · %s · von %s · freigegeben von %s</span><form method=post action=/inbox/%d/rollback><button>Zurücknehmen</button></form></div>", p.ID, html.EscapeString(p.Reason), html.EscapeString(p.Actor), html.EscapeString(p.DecidedBy), p.ID)
			}
			for _, p := range all {
				if p.Status == "stale" {
					fmt.Fprintf(&b, "<p class=gelb>#%d ist veraltet — neu vorschlagen: %s</p>", p.ID, html.EscapeString(p.StaleReason))
				}
			}
		}
		page(w, "Inbox", b.String())
	}))
	mux.HandleFunc("POST /inbox/{id}/{decision}", guard(func(w http.ResponseWriter, r *http.Request) {
		// Authelia hands the user in Remote-User; without it the actor is the web itself.
		actor := "human:" + r.Header.Get("Remote-User")
		if actor == "human:" {
			actor = "human:web"
		}
		webEnv := &cli.Env{Paths: env.Paths, Actor: actor}
		var code int
		if r.PathValue("decision") == "rollback" {
			code = inbox.Rollback(r.Context(), webEnv, []string{"#" + r.PathValue("id")})
		} else {
			code = inbox.Decide(r.Context(), webEnv, []string{r.PathValue("id")}, r.PathValue("decision") == "approve")
		}
		if code != cli.ExitOK && code != cli.ExitRed {
			page(w, "Inbox", fmt.Sprintf("<p class=rot>Entscheidung fehlgeschlagen (exit %d). Siehe Journal.</p><p><a href=/inbox>Inbox</a></p>", code))
			return
		}
		http.Redirect(w, r, "/journal", http.StatusSeeOther)
	}))
	// "jetzt prüfen" (#309): the service's check, started without waiting
	mux.HandleFunc("POST /check/{service}", guard(func(w http.ResponseWriter, r *http.Request) {
		st, err := cli.LoadState(env)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		svc, ok := st.Services[r.PathValue("service")]
		if !ok || !diag.Refresh(r.Context(), svc) {
			http.Error(w, "kein Check für diesen Dienst", http.StatusNotFound)
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}))
	mux.HandleFunc("/journal", guard(func(w http.ResponseWriter, r *http.Request) {
		es, err := journal.Read(env.Paths.Journal, time.Now().Add(-7*24*time.Hour))
		var b strings.Builder
		b.WriteString(nav + "<h1>Journal · 7 Tage</h1>")
		if err != nil {
			b.WriteString("<p class=rot>" + html.EscapeString(err.Error()) + "</p>")
		}
		b.WriteString(journalBody(es))
		page(w, "Journal", b.String())
	}))

	// the network as the rendered config makes it (#439), one card per name
	mux.HandleFunc("/net", guard(func(w http.ResponseWriter, r *http.Request) {
		st, err := cli.LoadState(env)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		page(w, "Netz", nav+"<h1>Netz</h1>"+netBody(render.Net(st)))
	}))

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
			BootUnclean = fmt.Sprintf("boot: %d rot, %d Dateien Drift", r.Red, r.Drift)
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

func resultClass(r string) string {
	switch r {
	case "green":
		return "grün"
	case "red":
		return "rot"
	}
	return "info"
}

// nav is the three pages' links, on top of each.
const nav = `<nav><a href="/">Status</a><a href="/inbox">Inbox</a><a href="/journal">Journal</a><a href="/net">Netz</a></nav>`

func netBody(n render.NetMap) string {
	var b strings.Builder
	for _, r := range n.Names {
		auth := "meldet sich selbst an"
		if r.ForwardAuth {
			auth = "Authelia vorab: " + strings.TrimSpace(r.Policy+" "+r.Subject)
		}
		fmt.Fprintf(&b, "<div class=card><b>%s</b><span class=state>%s</span><span class=note>%s · %s</span></div>\n", html.EscapeString(r.Name), r.Reach, html.EscapeString(auth), html.EscapeString(r.Upstream))
	}
	b.WriteString("<h2>Ports</h2>")
	for _, p := range n.Ports {
		fmt.Fprintf(&b, "<div class=card><b>%d</b><span class=state>%s</span><span class=note>%s</span></div>\n", p.Port, p.Reach, html.EscapeString(p.Service))
	}
	for _, l := range n.Links {
		fmt.Fprintf(&b, "<p class=muted>%s → %s über host.containers.internal:%d</p>", html.EscapeString(l.From), html.EscapeString(l.To), l.Port)
	}
	return b.String()
}

//go:embed laib.css
var css string

func page(w http.ResponseWriter, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, pageHTML(title, body))
}

// pageHTML is a page in the household look (#382): the CSS inline from the
// one embedded file, no request beyond the page itself.
func pageHTML(title, body string) string {
	return `<!doctype html><html lang=de><head><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1"><meta name=color-scheme content="dark light"><title>Laib · ` +
		html.EscapeString(title) + "</title><style>\n" + css + "</style></head><body>" + body + "</body></html>\n"
}

// statusBody is the status page: the box's line, one card per service with
// its state and note, the last change, the morning report.
func statusBody(rd Readiness, rows []ServiceStatus, checking map[string]bool, last string, report [2]string, bote bool) string {
	var b strings.Builder
	for _, on := range checking {
		if on {
			b.WriteString(`<meta http-equiv=refresh content=10>`)
			break
		}
	}
	links := `<a href="/inbox">Inbox</a><a href="/journal">Journal</a><a href="/net">Netz</a>`
	if bote {
		links += `<a href="/bote/link">Bote verbinden</a>`
	}
	heard := rd.LastHeard
	if len(heard) >= 19 {
		heard = heard[11:19]
	}
	fmt.Fprintf(&b, "<nav>%s</nav><h1>Laib · %s · %d Dienste</h1><p class=muted>Operator %s · zuletzt gehört %s</p>", links, html.EscapeString(rd.State), rd.Services, html.EscapeString(rd.Operator), heard)
	if rd.Lock != "" {
		fmt.Fprintf(&b, "<p class=gelb>gesperrt durch %s</p>", html.EscapeString(rd.Lock))
	}
	for _, row := range rows {
		var bad []string
		for u, a := range row.Units {
			if a != "active" {
				bad = append(bad, u+"="+a)
			}
		}
		sort.Strings(bad)
		note := strings.TrimSpace(strings.Join(bad, " ") + " " + row.Detail)
		check := ""
		if on, ok := checking[row.Name]; ok {
			check = `<form method=post action="/check/` + html.EscapeString(row.Name) + `"><button>jetzt prüfen</button></form>`
			if on {
				check = "<span class=muted>wird geprüft …</span>"
			}
		}
		fmt.Fprintf(&b, "<div class=\"card %s\"><b>%s</b><span class=state>%s</span><span class=note>%s</span>%s</div>\n", row.State, html.EscapeString(row.Name), row.State, html.EscapeString(note), check)
	}
	if last != "" {
		fmt.Fprintf(&b, "<p class=muted>Letzte Änderung %s</p>", html.EscapeString(last))
	}
	if report[0] != "" {
		fmt.Fprintf(&b, "<h2>Morgenbericht %s</h2><pre>%s</pre>", html.EscapeString(report[0]), html.EscapeString(report[1]))
	}
	return b.String()
}

// journalBody is the journal's entries, newest first, one row card each.
func journalBody(es []journal.Entry) string {
	var b strings.Builder
	for i := len(es) - 1; i >= 0; i-- {
		e := es[i]
		fmt.Fprintf(&b, "<div class=\"card %s\"><span class=muted>%s</span><b>%s %s</b><span class=state>%s</span><span class=note>%s · %s %s</span></div>\n", resultClass(e.Result), e.T.Local().Format("02.01. 15:04:05"), e.Verb, html.EscapeString(e.Service), e.Result, html.EscapeString(e.Actor), html.EscapeString(e.Reason), html.EscapeString(e.Detail))
	}
	return b.String()
}
