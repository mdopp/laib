package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/pages"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/sysd"
)

// #696: the owner does not work at the console. On the box page (behind
// Authelia's second factor) „Schlüssel hinterlegen“ takes a key a template
// declares as external into secrets.age, and applies what uses it — the
// way `laib secret rotate` does. The value goes in only: it is never on a
// page, in the journal, in the state repo's history or in laib's log.
func TestTheBoxPageTakesAKeyAndAppliesWhatUsesIt(t *testing.T) {
	c := newCbox(t)
	svc := filepath.Join(c.env.Paths.State, "services", "whoami")
	b, _ := os.ReadFile(filepath.Join(svc, "laib.yml"))
	os.WriteFile(filepath.Join(svc, "laib.yml"), bytes.Replace(b, []byte("secrets:\n"), []byte("secrets:\n  - name: WHOAMI_KEY\n    external: true\n    optional: true\n    owner: {label: \"Whoami: Testschlüssel\", warum: \"Damit grüßt whoami mit Namen.\", hilfe: \"Bei whoami.example unter Konto anlegen.\", link: \"https://whoami.example/konto\"}\n  - name: WHOAMI_OTHER\n    external: true\n    optional: true\n    owner: {label: \"Whoami: Zweiter Schlüssel\", warum: \"Noch einer.\", hilfe: \"Ebenda.\"}\n  - name: WHOAMI_MINTED\n    external: true\n    optional: true\n"), 1), 0o644)
	b, _ = os.ReadFile(filepath.Join(svc, "values.yml"))
	os.WriteFile(filepath.Join(svc, "values.yml"), append(b, []byte("KEY: !secret WHOAMI_KEY\n")...), 0o644)
	b, _ = os.ReadFile(filepath.Join(svc, "whoami.container"))
	os.WriteFile(filepath.Join(svc, "whoami.container"), bytes.Replace(b, []byte("Environment=TOKEN=${TOKEN}\n"), []byte("Environment=TOKEN=${TOKEN}\nEnvironment=KEY=${KEY}\n"), 1), 0o644)
	var log bytes.Buffer
	c.env.ErrOut = &log

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	t.Setenv("LAIB_LISTEN", addr)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pages.Serve(ctx, c.env, nil)
	sec, _ := secrets.Load(c.env.Paths.Secrets, c.env.Paths.Key)
	proxy := sec[render.ProxySecret]
	do := func(method, path string, form url.Values, hdr ...string) (int, string) {
		req, _ := http.NewRequest(method, "http://"+addr+path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Remote-User", "michael")
		req.Header.Set("X-Laib-Proxy", proxy)
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		resp, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			return 0, err.Error()
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	var page string
	until(t, "serve", func() bool { code, body := do("GET", "/schluessel", nil); page = body; return code == http.StatusOK })
	// the overview: per service, the owner's keys in the template's words, the name in small print
	for _, w := range []string{"Schlüssel hinterlegen", "<h2>Testseite</h2>", "Whoami: Testschlüssel", "Damit grüßt whoami mit Namen.", "<small>WHOAMI_KEY</small>", "fehlt", `href="/schluessel?name=WHOAMI_KEY"`, "Whoami: Zweiter Schlüssel"} {
		if !strings.Contains(page, w) {
			t.Errorf("the overview lacks %q:\n%s", w, page)
		}
	}
	// generated, minted by a hook, or not marked owner: never offered
	for _, w := range []string{render.ProxySecret, "WHOAMI_TOKEN", "WHOAMI_MINTED", "OIDC_", "DNS_API_TOKEN", "type=password"} {
		if strings.Contains(page, w) {
			t.Errorf("the overview offers or shows %q:\n%s", w, page)
		}
	}
	if _, home := do("GET", "/", nil); !strings.Contains(home, `href="/schluessel"`) {
		t.Errorf("the Status page has no way to the form:\n%s", home)
	}
	// one key's own page, the deep link Haus' settings open (docs/INTERFACE.md)
	back := "https://haus.example.home/discogs"
	_, one := do("GET", "/schluessel?"+url.Values{"name": {"WHOAMI_KEY"}, "zurueck": {back}}.Encode(), nil)
	for _, w := range []string{"<h1>Whoami: Testschlüssel</h1>", "für Testseite", "fehlt", "Damit grüßt whoami mit Namen.", "Bei whoami.example unter Konto anlegen.", `href="https://whoami.example/konto"`, `href="` + back + `">Zurück</a>`, `name=zurueck value="` + back + `"`, "type=password", "autocomplete=off", "<small>WHOAMI_KEY</small>"} {
		if !strings.Contains(one, w) {
			t.Errorf("the key's own page lacks %q:\n%s", w, one)
		}
	}
	if strings.Contains(one, "Zweiter") {
		t.Errorf("the key's own page shows another key:\n%s", one)
	}
	for _, evil := range []string{"https://evil.example/", "http://haus.example.home/", "//evil.example/", "https://example.home.evil.example/", "javascript:alert(1)"} {
		if _, p := do("GET", "/schluessel?"+url.Values{"name": {"WHOAMI_KEY"}, "zurueck": {evil}}.Encode(), nil); strings.Contains(p, "Zurück") || strings.Contains(p, "evil") || strings.Contains(p, "javascript") {
			t.Errorf("zurueck=%s is offered:\n%s", evil, p)
		}
	}
	if _, p := do("GET", "/schluessel?name="+render.ProxySecret, nil); !strings.Contains(p, "nicht setzen") || strings.Contains(p, "type=password") {
		t.Errorf("a key the page may not set has a form:\n%s", p)
	}

	const value = "dg-696-Wert-nie-zeigen"
	// the gate: only through Caddy and Authelia, and only from the box page itself
	if code, _ := do("POST", "/schluessel", url.Values{"name": {"WHOAMI_KEY"}, "wert": {value}}, "X-Laib-Proxy", "falsch"); code != http.StatusForbidden {
		t.Errorf("without the proxy secret: %d, want 403", code)
	}
	if code, _ := do("POST", "/schluessel", url.Values{"name": {"WHOAMI_KEY"}, "wert": {value}}, "Sec-Fetch-Site", "cross-site"); code != http.StatusForbidden {
		t.Errorf("a form from another site: %d, want 403", code)
	}
	if mcp.Allowed["secret"] {
		t.Error("an agent's MCP reaches the secret verb")
	}
	var bodies []string
	// a name the form does not offer is refused, the box's own secret unchanged
	_, body := do("POST", "/schluessel", url.Values{"name": {render.ProxySecret}, "wert": {value}})
	if bodies = append(bodies, body); !strings.Contains(body, "nicht") {
		t.Errorf("a name the form does not offer:\n%s", body)
	}
	code, body := do("POST", "/schluessel", url.Values{"name": {"WHOAMI_KEY"}, "wert": {"  "}})
	if code != http.StatusOK || !strings.Contains(body, "Kein Schlüssel eingegeben") {
		t.Errorf("an empty value: %d\n%s", code, body)
	}
	now, _ := secrets.Load(c.env.Paths.Secrets, c.env.Paths.Key)
	if now[render.ProxySecret] != proxy || now["WHOAMI_KEY"] != "" {
		t.Fatal("a refused form changed secrets.age")
	}

	// #768: while whoami restarts, the page says what the box does now, how long it takes, and reloads itself
	var during []string
	run := sysd.Run
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "systemctl" && slices.Contains(args, "restart") && slices.Contains(args, "whoami.service") {
			_, p := do("GET", "/schluessel?name=WHOAMI_KEY", nil)
			during = append(during, p)
		}
		return run(ctx, name, args...)
	}
	c.calls = nil
	code, body = do("POST", "/schluessel", url.Values{"name": {"WHOAMI_KEY"}, "wert": {value + "\n"}, "zurueck": {back}})
	bodies = append(bodies, body)
	if code != http.StatusOK && code != http.StatusSeeOther {
		t.Fatalf("saving: %d\n%s", code, body)
	}
	until(t, "applied", func() bool {
		_, body := do("GET", "/schluessel?"+url.Values{"name": {"WHOAMI_KEY"}, "zurueck": {back}}.Encode(), nil)
		bodies = append(bodies, body)
		return strings.Contains(body, "fertig: Testseite läuft mit dem neuen Schlüssel") && strings.Contains(body, ">Zurück</a>")
	})
	if len(during) == 0 {
		t.Fatal("whoami was not restarted")
	}
	bodies = append(bodies, during...)
	for _, w := range []string{"Schlüssel gespeichert ✓", "startet whoami …", "unter einer Minute", "Du musst nichts tun, die Seite aktualisiert sich", "http-equiv=refresh"} {
		if !strings.Contains(during[0], w) {
			t.Errorf("while whoami restarts the page lacks %q:\n%s", w, during[0])
		}
	}
	if done := bodies[len(bodies)-len(during)-1]; !strings.Contains(done, "prüft, ob whoami läuft ✓") || strings.Contains(done, "http-equiv=refresh") || strings.Contains(done, "Du musst nichts tun") {
		t.Errorf("done does not show its steps, or still reloads:\n%s", done)
	}
	if _, page = do("GET", "/schluessel", nil); !strings.Contains(page, "hinterlegt") || !strings.Contains(page, ">Ersetzen</a>") {
		t.Errorf("the overview does not say the key is there:\n%s", page)
	}
	now, _ = secrets.Load(c.env.Paths.Secrets, c.env.Paths.Key)
	if now["WHOAMI_KEY"] != value {
		t.Fatalf("secrets.age has %q", now["WHOAMI_KEY"])
	}
	if !slices.Contains(c.restarts(), "whoami.service") {
		t.Errorf("whoami was not restarted with its key: %v", c.restarts())
	}
	if !c.journalHas(t, "WHOAMI_KEY") {
		t.Error("the journal does not say which key was set")
	}
	if out, _ := exec.Command("git", "-C", c.env.Paths.State, "status", "--porcelain", "secrets.age").Output(); len(out) != 0 {
		t.Errorf("secrets.age is not committed on main (#328): %s", out)
	}

	// #768: a restart that goes red says so and why, and stops reloading
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "systemctl" && slices.Contains(args, "is-active") && slices.Contains(args, "whoami.service") {
			return "failed", nil
		}
		return run(ctx, name, args...)
	}
	do("POST", "/schluessel", url.Values{"name": {"WHOAMI_KEY"}, "wert": {value + "-2"}})
	until(t, "red", func() bool {
		_, body := do("GET", "/schluessel?name=WHOAMI_KEY", nil)
		bodies = append(bodies, body)
		return strings.Contains(body, "fehlgeschlagen, weil whoami danach nicht richtig lief") && !strings.Contains(body, "http-equiv=refresh")
	})
	sysd.Run = run

	// never shown, never journaled, never logged, never in a commit message
	journalFiles, _ := filepath.Glob(filepath.Join(c.env.Paths.Journal, "*"))
	var journalText []byte
	for _, f := range journalFiles {
		b, _ := os.ReadFile(f)
		journalText = append(journalText, b...)
	}
	history, _ := exec.Command("git", "-C", c.env.Paths.State, "log", "-p", "--all").Output()
	for what, text := range map[string]string{"a page": strings.Join(bodies, "\n"), "the journal": string(journalText), "laib's log": log.String(), "the state repo": string(history)} {
		if strings.Contains(text, value) {
			t.Errorf("the value is in %s", what)
		}
	}
}
