package pages

import (
	"cmp"
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
)

// keyState is what the form says about a key taken (#696), per name: on
// its way, or how it ended. Never the value.
var keyState = struct {
	sync.Mutex
	m map[string]string
}{m: map[string]string{}}

func setKeyState(name, s string) {
	keyState.Lock()
	defer keyState.Unlock()
	keyState.m[name] = s
}

// keyFor: the owner's key of that name (#696), or nil when the page may
// not set it — generated, minted, or not marked owner: by a template.
func keyFor(st *state.State, name string) *render.OwnerSecret {
	keys := render.OwnerSecrets(st)
	if i := slices.IndexFunc(keys, func(k render.OwnerSecret) bool { return k.Name == name }); i >= 0 {
		return &keys[i]
	}
	return nil
}

// backTo: zurueck= when it stays on this household's pages, https on the
// box's domain or a name under it (Haus' Discogs settings), else "": never
// an open redirect.
func backTo(st *state.State, raw string) string {
	u, err := url.Parse(raw)
	switch {
	case raw == "" || err != nil || strings.ContainsAny(raw, "\\\r\n"):
		return ""
	case u.Scheme == "https" && u.User == nil && (u.Hostname() == st.Box.Domain || strings.HasSuffix(u.Hostname(), "."+st.Box.Domain)):
		return raw
	}
	return ""
}

// Keys is „Schlüssel hinterlegen“ (#696): GET the form, POST takes the
// value into secrets.age and applies what uses it (apply.SetSecret, as
// `laib secret rotate`), detached from the request. It sits behind the box
// page's guard like Freigeben, refuses a form from another site, and no
// agent token reaches it (the secret verb is not in mcp.Allowed).
func Keys(env *cli.Env) http.HandlerFunc {
	cop := http.NewCrossOriginProtection()
	return func(w http.ResponseWriter, r *http.Request) {
		st, err := cli.LoadState(env)
		var sec map[string]string
		if err == nil {
			sec, err = secrets.Load(env.Paths.Secrets, env.Paths.Key)
		}
		if err != nil {
			page(w, "Schlüssel", "<p class=rot>State nicht lesbar: "+html.EscapeString(err.Error())+"</p>")
			return
		}
		name, back, note := r.FormValue("name"), backTo(st, r.FormValue("zurueck")), ""
		if r.Method == http.MethodPost {
			if err := cop.Check(r); err != nil {
				http.Error(w, "nur von der Box-Seite selbst", http.StatusForbidden)
				return
			}
			value, actor, key := strings.TrimSpace(r.PostFormValue("wert")), webActor(r), keyFor(st, name)
			switch {
			case key == nil:
				note = "<p class=rot>Diesen Schlüssel kann die Box-Seite nicht setzen.</p>"
			case value == "":
				note = "<p class=rot>Kein Schlüssel eingegeben.</p>"
			case !policy.Allowed(st.Policy, "human", "apply"):
				note = "<p class=rot>Dieses Konto darf keine Schlüssel setzen.</p>"
			default:
				setKeyState(name, "wird gespeichert …")
				go storeKey(env, actor, name, key.Owner.Label, value)
				http.Redirect(w, r, "/schluessel?"+url.Values{"name": {name}, "zurueck": {back}}.Encode(), http.StatusSeeOther)
				return
			}
		}
		if name != "" {
			page(w, "Schlüssel", note+keyForm(st, sec, keyFor(st, name), back))
			return
		}
		page(w, "Schlüssel", note+keysBody(st, sec))
	}
}

// storeKey waits for the work before it, then sets the key and applies
// its services. What it says afterwards never carries the value.
func storeKey(env *cli.Env, actor, name, label, value string) {
	for until := time.Now().Add(15 * time.Minute); apply.LockHolder(env.Paths) != "" && time.Now().Before(until); time.Sleep(time.Second) {
		setKeyState(name, "wartet, bis die Box mit der Arbeit davor fertig ist")
	}
	var out strings.Builder
	webEnv := &cli.Env{Paths: env.Paths, Actor: actor, Out: &out, ErrOut: &out}
	st, err := cli.LoadState(webEnv)
	if err != nil {
		setKeyState(name, "fehlgeschlagen: "+err.Error())
		return
	}
	users, code := apply.SetSecret(context.Background(), webEnv, st, name, value, "secret set "+name+" (Box-Seite)")
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(out.String(), value, "…")), "\n")
	for i, u := range users {
		users[i] = proposal.Title(u)
	}
	switch {
	case code != cli.ExitOK && len(users) == 0:
		setKeyState(name, "fehlgeschlagen, nicht gespeichert: "+strings.TrimPrefix(lines[len(lines)-1], "laib: "))
	case code != cli.ExitOK:
		setKeyState(name, label+" hinterlegt, aber "+strings.Join(users, ", ")+" läuft danach nicht richtig: "+strings.TrimPrefix(lines[len(lines)-1], "laib: "))
	case len(users) == 0:
		setKeyState(name, label+" hinterlegt, kein Dienst muss neu starten")
	default:
		setKeyState(name, label+" hinterlegt, "+strings.Join(users, ", ")+" neu gestartet")
	}
}

// keyLine: how saving one key went, while it runs or once it ended; it
// reloads the page while it runs. Never the value.
func keyLine(name string) string {
	keyState.Lock()
	defer keyState.Unlock()
	s, ok := keyState.m[name]
	switch {
	case !ok:
		return ""
	case strings.Contains(s, "fehlgeschlagen") || strings.Contains(s, "nicht richtig"):
		return "<p class=rot>" + html.EscapeString(s) + "</p>"
	case strings.HasSuffix(s, "…") || strings.HasPrefix(s, "wartet"):
		return `<meta http-equiv=refresh content=3><p class=gelb>` + html.EscapeString(s) + "</p>"
	}
	return "<p class=grün>" + html.EscapeString(s) + "</p>"
}

// keyState words: hinterlegt or fehlt, never the value.
func there(sec map[string]string, name string) (string, string) {
	if sec[name] != "" {
		return "grün", "hinterlegt"
	}
	return "gelb", "fehlt"
}

// keysBody is the overview: per service the keys its template asks the
// owner for, in the template's words, each with what it brings and a way
// to its own form; the env name only in small print.
func keysBody(st *state.State, sec map[string]string) string {
	var b strings.Builder
	b.WriteString("<h1>Schlüssel hinterlegen</h1><p>Manche Dienste brauchen einen Schlüssel, den du woanders bekommst, etwa bei Discogs. Hier sind sie alle, mit dem, wofür sie gut sind und woher du sie bekommst. Die Box speichert einen Schlüssel verschlüsselt, zeigt ihn nie wieder an und startet danach den Dienst neu, der ihn braucht.</p>")
	group := "-"
	for _, k := range render.OwnerSecrets(st) {
		if k.Service != group {
			group = k.Service
			fmt.Fprintf(&b, "<h2>%s</h2>", html.EscapeString(cmp.Or(proposal.Title(group), "Die Box")))
		}
		class, word := there(sec, k.Name)
		fmt.Fprintf(&b, `<div class="card %s"><b>%s</b><span class=state>%s</span><span class=note>%s<br><small>%s</small></span><a href="/schluessel?name=%s">%s</a></div>%s`,
			class, html.EscapeString(k.Owner.Label), word, html.EscapeString(k.Owner.Warum), html.EscapeString(k.Name), url.QueryEscape(k.Name), map[string]string{"fehlt": "Hinterlegen", "hinterlegt": "Ersetzen"}[word], keyLine(k.Name))
	}
	if group == "-" {
		b.WriteString("<p>Kein Dienst auf dieser Box braucht einen Schlüssel von dir.</p>")
	}
	return b.String()
}

// keyForm is one key's own page (/schluessel?name=…, #696): what it is,
// what it brings, where to get it, whether it is there, and the field.
// Other apps link here (docs/INTERFACE.md); zurueck leads back to them.
func keyForm(st *state.State, sec map[string]string, k *render.OwnerSecret, back string) string {
	var b strings.Builder
	links := `<p><a href="/schluessel">Alle Schlüssel</a>`
	if back != "" {
		links += ` · <a href="` + html.EscapeString(back) + `">Zurück</a>`
	}
	if k == nil {
		return "<h1>Schlüssel hinterlegen</h1><p class=rot>Diesen Schlüssel kann die Box-Seite nicht setzen.</p>" + links + "</p>"
	}
	class, word := there(sec, k.Name)
	fmt.Fprintf(&b, `<h1>%s</h1><p>für %s · <span class=%s>%s</span></p><p>%s</p><p>%s`, html.EscapeString(k.Owner.Label), html.EscapeString(cmp.Or(proposal.Title(k.Service), "die Box")), class, word, html.EscapeString(k.Owner.Warum), html.EscapeString(k.Owner.Hilfe))
	if l := strings.ReplaceAll(k.Owner.Link, "${DOMAIN}", st.Box.Domain); l != "" {
		fmt.Fprintf(&b, ` <a href="%s" target=_blank rel=noopener>%s</a>`, html.EscapeString(l), html.EscapeString(strings.TrimPrefix(l, "https://")))
	}
	b.WriteString("</p>" + keyLine(k.Name))
	button := map[string]string{"fehlt": "Hinterlegen", "hinterlegt": "Ersetzen"}[word]
	fmt.Fprintf(&b, `<form method=post action=/schluessel onsubmit="const b=this.querySelector('button');b.disabled=true;b.textContent='wird gespeichert …'"><input type=hidden name=name value="%s"><input type=hidden name=zurueck value="%s"><p><label>Schlüssel<br><input type=password name=wert autocomplete=off required placeholder="hier einfügen"></label></p><p><button class=go>%s</button></p></form><p>Die Box speichert ihn verschlüsselt, zeigt ihn nie wieder an und startet danach %s neu.<br><small>%s</small></p>`,
		html.EscapeString(k.Name), html.EscapeString(back), button, html.EscapeString(cmp.Or(proposal.Title(k.Service), "die Dienste, die ihn brauchen,")), html.EscapeString(k.Name))
	return b.String() + links + "</p>"
}
