package pages

import (
	"cmp"
	"context"
	"fmt"
	"html"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/policy"
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

// keyChoices are the keys the owner brings from outside (#696): declared
// external everywhere, not a token Laib mints itself — the ones the box
// makes or mints are never offered. Missing ones first.
func keyChoices(st *state.State, sec map[string]string) []string {
	made := map[string]bool{render.OperatorToken: true}
	var out []string
	for _, d := range render.SecretNames(st) {
		made[d.Name] = made[d.Name] || !d.External || d.Mint != ""
		if !slices.Contains(out, d.Name) {
			out = append(out, d.Name)
		}
	}
	out = slices.DeleteFunc(out, func(n string) bool { return made[n] })
	slices.SortStableFunc(out, func(a, b string) int { return cmp.Compare(min(len(sec[a]), 1), min(len(sec[b]), 1)) })
	return out
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
		choices, note := keyChoices(st, sec), ""
		if r.Method == http.MethodPost {
			if err := cop.Check(r); err != nil {
				http.Error(w, "nur von der Box-Seite selbst", http.StatusForbidden)
				return
			}
			name, value := r.PostFormValue("name"), strings.TrimSpace(r.PostFormValue("wert"))
			actor := webActor(r)
			switch {
			case !slices.Contains(choices, name):
				note = "<p class=rot>Diesen Schlüssel kann die Box-Seite nicht setzen.</p>"
			case value == "":
				note = "<p class=rot>Kein Schlüssel eingegeben.</p>"
			case !policy.Allowed(st.Policy, "human", "apply"):
				note = "<p class=rot>Dieses Konto darf keine Schlüssel setzen.</p>"
			default:
				setKeyState(name, "wird gespeichert …")
				go storeKey(env, actor, name, value)
				http.Redirect(w, r, "/schluessel", http.StatusSeeOther)
				return
			}
		}
		page(w, "Schlüssel", keysBody(st, sec, choices, r.FormValue("name"), note))
	}
}

// storeKey waits for the work before it, then sets the key and applies
// its services. What it says afterwards never carries the value.
func storeKey(env *cli.Env, actor, name, value string) {
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
	switch {
	case code != cli.ExitOK && len(users) == 0:
		setKeyState(name, "fehlgeschlagen, nicht gespeichert: "+strings.TrimPrefix(lines[len(lines)-1], "laib: "))
	case code != cli.ExitOK:
		setKeyState(name, name+" gespeichert, aber "+strings.Join(users, ", ")+" ist nicht grün: "+strings.TrimPrefix(lines[len(lines)-1], "laib: "))
	case len(users) == 0:
		setKeyState(name, name+" gespeichert, kein Dienst muss neu starten")
	default:
		setKeyState(name, name+" gespeichert, "+strings.Join(users, ", ")+" neu gestartet")
	}
}

// keysBody is the form: one choice per key, which services use it and
// whether it is there; a password field the value only goes into.
func keysBody(st *state.State, sec map[string]string, choices []string, selected, note string) string {
	var b strings.Builder
	b.WriteString("<h1>Schlüssel hinterlegen</h1><p>Ein Zugangsschlüssel, den du bei einem Dienst erzeugt hast (etwa bei Discogs), kommt hier auf die Box. Sie speichert ihn verschlüsselt, zeigt ihn nie wieder an und startet danach den Dienst neu, der ihn braucht.</p>" + note)
	keyState.Lock()
	for _, name := range slices.Sorted(maps.Keys(keyState.m)) {
		s, class := keyState.m[name], "grün"
		switch {
		case strings.Contains(s, "fehlgeschlagen") || strings.Contains(s, "nicht grün"):
			class = "rot"
		case strings.HasSuffix(s, "…") || strings.HasPrefix(s, "wartet"):
			class = "gelb"
			b.WriteString(`<meta http-equiv=refresh content=3>`)
		}
		fmt.Fprintf(&b, "<p class=%s>%s</p>", class, html.EscapeString(s))
	}
	keyState.Unlock()
	if len(choices) == 0 {
		return b.String() + "<p>Keine Vorlage auf dieser Box braucht einen Schlüssel von außen.</p>"
	}
	b.WriteString(`<form method=post action=/schluessel onsubmit="const b=this.querySelector('button');b.disabled=true;b.textContent='wird gespeichert …'"><p><label>Welcher Schlüssel<br><select name=name>`)
	for _, name := range choices {
		users := cmp.Or(strings.Join(render.SecretUsers(st, name), ", "), "die Box")
		there := "fehlt"
		if sec[name] != "" {
			there = "gesetzt, wird ersetzt"
		}
		sel := ""
		if name == selected {
			sel = " selected"
		}
		fmt.Fprintf(&b, "<option value=\"%s\"%s>%s · für %s · %s</option>", html.EscapeString(name), sel, html.EscapeString(name), html.EscapeString(users), there)
	}
	b.WriteString(`</select></label></p><p><label>Schlüssel<br><input type=password name=wert autocomplete=off required placeholder="hier einfügen"></label></p><p><button class=go>Speichern und anwenden</button> <a href="/">Abbrechen</a></p></form>`)
	return b.String()
}
