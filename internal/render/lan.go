package render

import (
	"cmp"
	"fmt"
	"html"
	"slices"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/state"
)

// The page at the box's bare LAN address (#1043): http://<lanIp>/, for a
// client on the LAN only, no login, no request beyond the page, so it
// answers while the names (AdGuard), Authelia or Laib itself are down.
// Caddy serves it as a file from render/status/: laib serve writes
// index.html with the status every LanEvery; Render leaves fallback.html,
// the same page without a status, for before the first one. Read-only:
// titles, states, addresses, no secret, no log, no person, no button.

// LanEvery is how often laib serve writes the page. The reader's browser
// calls it live while its Stand is younger than lanLive (a write, its
// status and a margin), else old: a static file never claims to be live.
const (
	LanEvery = time.Minute
	lanLive  = 150 // seconds
)

// lanSite is the address's site: http only, no certificate is valid for an
// address, so nothing is on 443 (ADR 0007, #1043); the LAN only.
func lanSite(st *state.State) string {
	if st.Box.LanIP == "" {
		return ""
	}
	return fmt.Sprintf("http://%s {\n\t@lan remote_ip private_ranges\n\thandle @lan {\n\t\troot * /srv/status\n\t\ttry_files /index.html /fallback.html\n\t\theader Cache-Control no-store\n\t\tfile_server\n\t}\n\timport lanonly\n}\n\n", st.Box.LanIP)
}

// lanFallback: the page before laib serve's first index.html, beside the Caddyfile
func lanFallback(st *state.State) Output {
	if st.Box.Roles.Proxy == "" || st.Box.LanIP == "" {
		return nil
	}
	return Output{"status/fallback.html": LanPage(st, nil)}
}

// LanStatus is what laib serve read; nil is the page before the first.
type LanStatus struct {
	At       time.Time
	Version  string
	Uptime   time.Duration
	Problems int // red beyond one service: disk, backup, firewall
	Rows     []LanRow
}

type LanRow struct {
	Service, State string // grün | gelb | rot | pausiert
	Since          time.Time
}

// lanOrder: what needs a look first
var lanOrder = map[string]int{"rot": 0, "gelb": 1, "pausiert": 2, "grün": 3, "unbekannt": 4}

// LanPage is the page; s nil renders every service of the state with no state.
func LanPage(st *state.State, s *LanStatus) string {
	rows := []LanRow{}
	if s != nil {
		rows = s.Rows
	} else {
		for _, name := range st.ServiceNames() {
			rows = append(rows, LanRow{Service: name, State: "unbekannt"})
		}
	}
	title := func(name string) string {
		if svc, ok := st.Services[name]; ok {
			return cmp.Or(svc.Contract.Title, name)
		}
		return name
	}
	rows = slices.Clone(rows)
	slices.SortStableFunc(rows, func(a, b LanRow) int {
		return cmp.Or(cmp.Compare(lanOrder[a.State], lanOrder[b.State]), cmp.Compare(title(a.Service), title(b.Service)))
	})
	count := map[string]int{}
	for _, r := range rows {
		count[r.State]++
	}
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang=de><head><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1"><meta name=color-scheme content="dark light"><title>Box-Status</title><style>` + lanCSS + "</style></head>")
	if s == nil {
		b.WriteString("<body><h1>Box-Status</h1>")
	} else {
		fmt.Fprintf(&b, "<body data-stand=%q data-hm=%q data-hms=%q><h1>Box-Status</h1><p id=alt class=banner hidden></p>", s.At.UTC().Format(time.RFC3339), s.At.Local().Format("15:04"), s.At.Local().Format("02.01. 15:04:05"))
	}
	state, verdict := "grün", "Alles in Ordnung"
	switch {
	case s == nil:
		state, verdict = "unbekannt", "Noch kein Stand"
	case count["rot"] > 0:
		state, verdict = "rot", plural(count["rot"], "Dienst gestört", "Dienste gestört")
	case s.Problems > 0:
		state, verdict = "rot", "Alle Dienste laufen, aber die Box hat "+plural(s.Problems, "Problem", "Probleme")
	case count["gelb"] > 0:
		state, verdict = "gelb", plural(count["gelb"], "Dienst startet", "Dienste starten")+" gerade"
	}
	fmt.Fprintf(&b, "<section class=\"box %s\"><p class=verdict><span class=dot></span><b>%s</b></p>", state, verdict)
	if s == nil {
		b.WriteString("<p>Laib hat diese Seite noch nicht geschrieben. Läuft Laib, steht hier in einer Minute der Stand; sonst antwortet Laib nicht. Die Adressen unten gehen trotzdem.</p>")
	} else {
		// without JavaScript the time itself, prominent; the script says live or old
		fmt.Fprintf(&b, "<p id=frisch class=stand><b>Stand %s</b></p><noscript><p class=muted>Ob dieser Stand aktuell ist, prüft die Seite nur mit JavaScript: Laib schreibt sie jede Minute neu; ist die Uhrzeit älter, antwortet Laib nicht.</p></noscript>", s.At.Local().Format("02.01. 15:04:05"))
		fmt.Fprintf(&b, "<p class=muted>%d Dienste: %d grün, %d gelb, %d rot · Laib %s", len(rows), count["grün"]+count["pausiert"], count["gelb"], count["rot"], html.EscapeString(s.Version))
		if s.Uptime > 0 {
			b.WriteString(" · Box läuft seit " + lanSpan(s.Uptime))
		}
		b.WriteString("</p>")
	}
	if s != nil && s.Problems > 0 {
		b.WriteString("<p>Was genau, steht unter Verwalten.</p>")
	}
	fmt.Fprintf(&b, "<p><a class=verwalten href=\"https://%s/\">Verwalten</a> <span class=muted>%s, mit Anmeldung</span></p></section>", html.EscapeString(fqdn(st, st.Box.Laib.Host)), html.EscapeString(fqdn(st, st.Box.Laib.Host)))
	b.WriteString("<h2>Dienste</h2>")
	links := lanLinks(st)
	for _, r := range rows {
		desc := ""
		if svc, ok := st.Services[r.Service]; ok {
			desc = svc.Contract.Beschreibung
		}
		fmt.Fprintf(&b, "<section class=\"dienst %s\"><p><b>%s</b> <span class=state><span class=dot></span>%s</span>", r.State, html.EscapeString(title(r.Service)), r.State)
		if !r.Since.IsZero() {
			fmt.Fprintf(&b, " <small class=muted>seit %s</small>", r.Since.Local().Format("02.01. 15:04"))
		}
		b.WriteString("</p>")
		if desc != "" {
			b.WriteString("<p class=muted>" + html.EscapeString(desc) + "</p>")
		}
		if l := links[r.Service]; len(l) > 0 {
			b.WriteString("<p class=links>" + strings.Join(l, " ") + "</p>")
		}
		b.WriteString("</section>")
	}
	fmt.Fprintf(&b, "<p class=muted>Nur im WLAN zu Hause, ohne Anmeldung, nur zum Nachsehen. Die Adressen mit Doppelpunkt und Nummer gehen auch, wenn die Namen gerade nicht gehen. Laib schreibt diese Seite jede Minute neu.</p>")
	fmt.Fprintf(&b, "<script>%s</script></body></html>\n", strings.ReplaceAll(lanJS, "LIVE", fmt.Sprint(lanLive)))
	return b.String()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// lanSpan: "3 Tagen", "5 Stunden", "12 Minuten"
func lanSpan(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%d Tagen", int(d/(24*time.Hour)))
	case d >= 2*time.Hour:
		return fmt.Sprintf("%d Stunden", int(d/time.Hour))
	}
	return fmt.Sprintf("%d Minuten", int(d/time.Minute))
}

// lanLinks: each service's names, then what the LAN reaches by the box's
// address alone (Net's ports with reach LAN, not dropped by nftables), so
// the page works while the names do not: a route's port as a link to
// http://<lanIp>:<port>/ (Caddy speaks plain http to it, so it does), any
// other port as text, Samba as smb://<lanIp>. A service with a name and no
// such port says so; the page opens no port and passes no login (#1043).
func lanLinks(st *state.State) map[string][]string {
	lan := map[string][]int{}
	for _, p := range Net(st).Ports {
		if p.Reach == "LAN" {
			lan[p.Service] = append(lan[p.Service], p.Port)
		}
	}
	out, ip := map[string][]string{}, st.Box.LanIP
	for _, name := range st.ServiceNames() {
		c, seen, web, other := st.Services[name].Contract, map[string]bool{}, false, false
		add := func(s string) {
			if !seen[s] {
				seen[s] = true
				out[name] = append(out[name], s)
			}
		}
		for _, r := range c.Routes {
			add(fmt.Sprintf("<a href=\"https://%[1]s/\">%[1]s</a>", html.EscapeString(fqdn(st, r.Host))))
		}
		for _, r := range c.Routes {
			if slices.Contains(lan[name], r.Port) {
				web = true
				add(fmt.Sprintf("<a href=\"http://%[1]s:%[2]d/\">%[1]s:%[2]d</a>", html.EscapeString(ip), r.Port))
			}
		}
		for _, p := range lan[name] {
			if slices.ContainsFunc(c.Routes, func(r state.Route) bool { return r.Port == p }) {
				continue
			}
			other = true
			a := fmt.Sprintf("%s:%d", ip, p)
			switch p {
			case 139, 445: // Samba: the file share, in the Finder or the Explorer
				a = "smb://" + ip
			case 1883:
				a = "mqtt://" + a
			}
			add("<span class=muted>" + html.EscapeString(a) + "</span>")
		}
		switch {
		case len(c.Routes) > 0 && !web && other:
			add("<span class=muted>Webseite nur über den Namen</span>")
		case len(c.Routes) > 0 && !web:
			add("<span class=muted>nur über den Namen erreichbar</span>")
		case len(c.Routes) == 0 && !other:
			add("<span class=muted>keine Adresse zum Öffnen</span>")
		}
	}
	return out
}

// lanJS: the Stand's age against the reader's clock, every 15 seconds:
// live, or the old Stand's banner with the services dimmed as last known;
// every 30 seconds the page fetched again and swapped in when it is newer,
// so an open page stays live while laib writes.
const lanJS = `(function(){` +
	`function z(){var b=document.body,t=Date.parse(b.dataset.stand),f=document.getElementById('frisch'),a=document.getElementById('alt');if(!f||!a||isNaN(t))return;` +
	`var s=(Date.now()-t)/1000,live=s<LIVE;b.classList.toggle('veraltet',!live);a.hidden=live;f.className=live?'stand live':'stand';f.firstChild.textContent=(live?'Live · Stand ':'Stand ')+b.dataset.hms;` +
	`if(!live){var m=Math.floor(s/60);a.textContent='Alter Stand von '+b.dataset.hm+' – Laib antwortet seit '+(m<120?m+' Minuten':Math.floor(m/60)+' Stunden')+' nicht. Die Punkte zeigen den zuletzt bekannten Zustand, die Adressen gehen trotzdem.'}}` +
	`function n(){fetch(location.href,{cache:'no-store'}).then(function(r){return r.text()}).then(function(h){var d=new DOMParser().parseFromString(h,'text/html'),b=document.body;` +
	`if(d.body.dataset.stand&&d.body.dataset.stand!==b.dataset.stand){b.innerHTML=d.body.innerHTML;['stand','hm','hms'].forEach(function(k){b.dataset[k]=d.body.dataset[k]})}z()}).catch(z)}` +
	`z();setInterval(z,15000);setInterval(n,30000)})()`

const lanCSS = `:root{color-scheme:dark;--bg:#070709;--card:#14141a;--edge:rgba(255,255,255,.12);--ink:#f2f2f5;--muted:#9aa0ae;--faint:#6b7280;--accent:#f97316;--ok:#10b981;--warn:#f59e0b;--bad:#ef5350}` +
	`@media (prefers-color-scheme: light){:root{color-scheme:light;--bg:#f1f5f9;--card:#fff;--edge:rgba(15,23,42,.12);--ink:#0f172a;--muted:#64748b;--faint:#94a3b8;--accent:#ea580c;--ok:#059669;--warn:#b45309;--bad:#dc2626}}` +
	`*{box-sizing:border-box}html{background:var(--bg)}[hidden]{display:none!important}body{margin:0 auto;max-width:44rem;padding:8px 16px 32px;background:var(--bg);color:var(--ink);font:16px/1.5 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;overflow-wrap:anywhere}` +
	`h1{font-size:1.6rem;margin:22px 0 8px}h2{font-size:.72rem;font-weight:600;letter-spacing:.1em;text-transform:uppercase;color:var(--muted);margin:26px 2px 8px}p{margin:6px 0}a{color:var(--accent)}.muted{color:var(--muted)}` +
	`section{background:var(--card);border:1px solid var(--edge);border-left:4px solid var(--faint);border-radius:14px;padding:8px 14px;margin:8px 0}.grün{border-left-color:var(--ok)}.gelb{border-left-color:var(--warn)}.rot{border-left-color:var(--bad)}` +
	`.dot{display:inline-block;width:.7em;height:.7em;border-radius:50%;background:var(--faint);margin-right:6px}.grün .dot{background:var(--ok)}.gelb .dot{background:var(--warn)}.rot .dot{background:var(--bad)}` +
	`.state{font-size:.9rem;font-weight:600;margin-left:6px}.grün .state{color:var(--ok)}.gelb .state{color:var(--warn)}.rot .state{color:var(--bad)}.verdict{font-size:1.15rem}` +
	`.verwalten{display:inline-block;background:var(--accent);color:#fff;border-radius:999px;padding:10px 20px;margin:6px 8px 6px 0;font-weight:600;text-decoration:none}.links a{display:inline-block;margin-right:14px;padding:4px 0}.links span{margin-right:14px}.stand{font-size:1.15rem;margin:8px 0}.stand.live{color:var(--ok)}` +
	`.banner{background:var(--bad);color:#fff;border-radius:12px;padding:10px 14px;margin:8px 0;font-weight:600}.veraltet section .dot{opacity:.3}.veraltet .dienst{opacity:.75}.veraltet .dienst .state:before{content:"zuletzt bekannt: ";font-weight:400;color:var(--muted)}`
