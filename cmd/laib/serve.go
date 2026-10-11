package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/secrets"
)

// cmdServe is the daemon: /ready for Solaris and the pages, and the three
// pages (Status, Inbox, Journal) as plain server-rendered HTML. Caddy and
// Authelia sit in front of it; the listener is loopback only.
func cmdServe(ctx context.Context, env *cliEnv, args []string) int {
	addr := "127.0.0.1:5900"
	if st, err := loadState(env); err == nil {
		addr = fmt.Sprintf("%s:%d", st.Box.Laib.Listen, st.Box.Laib.Port)
	}
	if v := os.Getenv("LAIB_LISTEN"); v != "" {
		addr = v
	}
	// viaProxy: the pages trust Remote-User only behind Caddy's secret header.
	// Before a proxy role exists there is no secret and loopback is the only
	// listener, so the pages are open to the box operator (ADR 0012).
	viaProxy := func(r *http.Request) bool {
		sec, err := secrets.Load(env.paths.Secrets, env.paths.Key)
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
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", mcpHandler(env))
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		st, err := loadState(env)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"state": "eingeschränkt", "lock": "state: " + err.Error()})
			return
		}
		rd, _ := collectStatus(r.Context(), env, st)
		json.NewEncoder(w).Encode(rd)
	})
	mux.HandleFunc("/", guard(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		st, err := loadState(env)
		if err != nil {
			page(w, "Status", "<p class=rot>State nicht lesbar: "+html.EscapeString(err.Error())+"</p>")
			return
		}
		rd, rows := collectStatus(r.Context(), env, st)
		var b strings.Builder
		fmt.Fprintf(&b, "<h1>Laib · %s · %d Dienste · Operator %s · zuletzt gehört %s</h1>", rd.State, rd.Services, html.EscapeString(rd.Operator), rd.LastHeard[11:19])
		if rd.Lock != "" {
			fmt.Fprintf(&b, "<p>gesperrt durch %s</p>", html.EscapeString(rd.Lock))
		}
		b.WriteString("<table>")
		for _, row := range rows {
			var bad []string
			for u, a := range row.Units {
				if a != "active" {
					bad = append(bad, u+"="+a)
				}
			}
			fmt.Fprintf(&b, "<tr class=%s><td>%s</td><td>%s</td><td>%s</td></tr>", row.State, row.State, html.EscapeString(row.Name), html.EscapeString(strings.Join(bad, " ")))
		}
		b.WriteString("</table>")
		if e, ok := journal.Last(env.paths.Journal); ok {
			fmt.Fprintf(&b, "<p>Letzte Änderung %s · %s · %s %s · %s</p>", e.T.Local().Format("02.01. 15:04"), html.EscapeString(e.Actor), e.Verb, html.EscapeString(e.Service), html.EscapeString(e.Reason))
		}
		if day, text := lastReport(env); day != "" {
			fmt.Fprintf(&b, "<h2>Morgenbericht %s</h2><pre>%s</pre>", html.EscapeString(day), html.EscapeString(text))
		}
		b.WriteString(`<p><a href="/inbox">Inbox</a> · <a href="/journal">Journal</a></p>`)
		page(w, "Status", b.String())
	}))
	mux.HandleFunc("/inbox", guard(func(w http.ResponseWriter, r *http.Request) {
		open, err := proposal.List(inboxDir(env), true)
		var b strings.Builder
		b.WriteString("<h1>Inbox</h1>")
		if err != nil {
			b.WriteString("<p class=rot>" + html.EscapeString(err.Error()) + "</p>")
		}
		if len(open) == 0 {
			b.WriteString("<p>Keine offenen Vorschläge.</p>")
		}
		for _, p := range open {
			fmt.Fprintf(&b, "<h2>Vorschlag #%d · von %s · Stufe: %s · braucht: andere Hand mit %s</h2><p>%s</p><pre>%s</pre>",
				p.ID, html.EscapeString(p.Actor), p.Tier, p.Tier, html.EscapeString(p.Reason), html.EscapeString(proposal.Diff(r.Context(), env.paths.State, p)))
			for _, w := range p.Warnings {
				fmt.Fprintf(&b, "<p class=gelb>Hinweis: %s</p>", html.EscapeString(w))
			}
			fmt.Fprintf(&b, "<p>Snapshot vor Apply: ja · Rollback: laib rollback</p><form method=post action=/inbox/%d/approve style=display:inline><button>Freigeben</button></form> <form method=post action=/inbox/%d/reject style=display:inline><button>Ablehnen</button></form>", p.ID, p.ID)
		}
		b.WriteString("<p><a href=\"/\">Status</a> · <a href=\"/journal\">Journal</a></p>")
		page(w, "Inbox", b.String())
	}))
	mux.HandleFunc("POST /inbox/{id}/{decision}", guard(func(w http.ResponseWriter, r *http.Request) {
		// Authelia hands the user in Remote-User; without it the actor is the web itself.
		actor := "human:" + r.Header.Get("Remote-User")
		if actor == "human:" {
			actor = "human:web"
		}
		webEnv := &cliEnv{paths: env.paths, actor: actor}
		code := decide(r.Context(), webEnv, []string{r.PathValue("id")}, r.PathValue("decision") == "approve")
		if code != exitOK && code != exitRed {
			page(w, "Inbox", fmt.Sprintf("<p class=rot>Entscheidung fehlgeschlagen (exit %d). Siehe Journal.</p><p><a href=/inbox>Inbox</a></p>", code))
			return
		}
		http.Redirect(w, r, "/journal", http.StatusSeeOther)
	}))
	mux.HandleFunc("/journal", guard(func(w http.ResponseWriter, r *http.Request) {
		es, err := journal.Read(env.paths.Journal, time.Now().Add(-7*24*time.Hour))
		var b strings.Builder
		b.WriteString("<h1>Journal · 7 Tage</h1>")
		if err != nil {
			b.WriteString("<p class=rot>" + html.EscapeString(err.Error()) + "</p>")
		}
		b.WriteString("<table>")
		for i := len(es) - 1; i >= 0; i-- {
			e := es[i]
			fmt.Fprintf(&b, "<tr class=%s><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s %s</td></tr>", resultClass(e.Result), e.T.Local().Format("02.01. 15:04:05"), html.EscapeString(e.Actor), e.Verb, html.EscapeString(e.Service), e.Result, html.EscapeString(e.Reason), html.EscapeString(e.Detail))
		}
		b.WriteString("</table><p><a href=\"/\">Status</a></p>")
		page(w, "Journal", b.String())
	}))

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fail(err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	// Boot comparison (#9): bereit only once units and files match the state.
	if st, err := loadState(env); err == nil {
		r, _ := collectStatus(ctx, env, st)
		if r.Red > 0 || r.Drift > 0 {
			bootUnclean = fmt.Sprintf("boot: %d rot, %d Dateien Drift", r.Red, r.Drift)
			record(env, st, journal.Entry{Actor: "laib", Verb: "ready", Result: "red", Detail: bootUnclean})
		} else {
			record(env, st, journal.Entry{Actor: "laib", Verb: "ready", Result: "green", Detail: fmt.Sprintf("%d Dienste grün", r.Green)})
		}
	}
	journal.Append(env.paths.Journal, journal.Entry{Actor: "laib", Verb: "serve", Result: "info", Detail: "listening on " + addr})
	fmt.Fprintln(os.Stderr, "laib serve: listening on", addr)
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return fail(err)
	}
	return exitOK
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

func page(w http.ResponseWriter, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html lang=de><head><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1"><title>Laib · %s</title><style>
body{font:15px/1.5 system-ui,sans-serif;margin:1.5rem;max-width:60rem;color:#222;background:#faf8f3}
h1{font-size:1.2rem}table{border-collapse:collapse}td{padding:.2rem .8rem .2rem 0;vertical-align:top}
tr.grün td:first-child{color:#2a7}p.gelb{color:#c90}tr.gelb td:first-child{color:#c90}tr.rot td:first-child,p.rot{color:#c33}a{color:#246}
</style></head><body>%s</body></html>`, html.EscapeString(title), body)
}
