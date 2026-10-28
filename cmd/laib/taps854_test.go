package main

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/pages"
	"github.com/mdopp/laib/internal/proposal"
)

// #854: #588 as a gate. Every tap on Laib's pages ends visibly where it
// began: done, red with the next step, or waiting (data-run) with a stated
// limit. The table below holds every POST route laib serve has; a route it
// does not know fails TestEveryRouteOfThePagesIsInTheTapTable.

// routeKinds: every pattern Routes registers, and what it is. "tap" routes
// take a POST from a button and have their cases in tapCases.
var routeKinds = map[string]string{
	"/mcp":                        "machine: JSON-RPC for agents, behind tokens",
	"/v1/":                        "machine: the FleetLock of the OS updates",
	"/ready":                      "machine: JSON for Haus and the watchdog",
	"/":                           "page",
	"GET /stand":                  "fragment",
	"/abmelden":                   "redirect to Authelia's logout",
	"/inbox":                      "page",
	"/inbox/alle":                 "tap",
	"GET /inbox/stand":            "fragment",
	"/schluessel":                 "tap",
	"GET /schluessel/stand":       "fragment",
	"POST /inbox/{id}/{decision}": "tap",
	"POST /check/{service}":       "tap",
	"/journal":                    "page: the Details place, technical by intent",
}

func TestEveryRouteOfThePagesIsInTheTapTable(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "internal", "pages", "serve.go"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, m := range regexp.MustCompile(`mux\.HandleFunc\("([^"]+)"`).FindAllStringSubmatch(string(src), -1) {
		seen[m[1]] = true
		if _, ok := routeKinds[m[1]]; !ok {
			t.Errorf("route %q is not in the tap table (#854): say what it is, and if a button posts to it, add its cases", m[1])
		}
	}
	covered := map[string]bool{}
	for _, c := range tapCases {
		covered[c.route] = true
	}
	for route, kind := range routeKinds {
		if !seen[route] {
			t.Errorf("the tap table names %q, Routes has no such route", route)
		}
		if kind == "tap" && !covered[route] {
			t.Errorf("tap route %q has no case", route)
		}
	}
}

// technicalText is what never stands in front of a person (#855): a
// command, a unit, an exit code, a Go or HTTP error, English error words,
// a config file or a flag. Under Details it may.
var technicalText = regexp.MustCompile(`\blaib [a-z]|\blaib_[a-z]|\b[\w@-]+\.(service|timer|socket|container)\b|\bexit (status )?\d|dial tcp|connection refused|no such (file|host)|deadline exceeded|i/o timeout|\b(Get|Post) "http|\b[1-5]\d\d (OK|Conflict|Bad|Not|Internal|Service|Forbidden|Unauthorized|Too)\b|\b(failed|not found|is not|unknown|cannot|refused|invalid|error|no approve)\b|\b\w+\.ya?ml\b|(^|\s)--[a-z]|\{"`)

var (
	hidden = regexp.MustCompile(`(?s)<details.*?</details>|<script.*?</script>|<style.*?</style>|<noscript.*?</noscript>`)
	tags   = regexp.MustCompile(`(?s)<(?:[^>"']|"[^"]*"|'[^']*')*>`)
	reds   = regexp.MustCompile(`(?s)class="?(?:card )?rot"?[^>]*>(.*?)</(?:p|div)>`)
	// a next step: what the person can do now, in words
	nextStep = regexp.MustCompile(`(?i)noch einmal|versuch|lade|sieh|frag|journal|tipp|gib .* frei|neu vorschlagen|füge|zurück zur`)
)

// visibleText is what a person reads on a page or fragment: no Details,
// no script, no tags.
func visibleText(page string) string {
	return html.UnescapeString(tags.ReplaceAllString(hidden.ReplaceAllString(page, " "), " "))
}

// plainEnough fails on technical text in what a person reads, and on a red
// line without its next step (#855, #588).
func plainEnough(t *testing.T, where, page string) {
	t.Helper()
	if m := technicalText.FindString(visibleText(page)); m != "" {
		t.Errorf("%s shows technical text %q to a person:\n%s", where, m, visibleText(page))
	}
	for _, r := range reds.FindAllStringSubmatch(hidden.ReplaceAllString(page, " "), -1) {
		if line := visibleText(r[1]); !nextStep.MatchString(line) {
			t.Errorf("%s: a red line without its next step: %q", where, strings.TrimSpace(line))
		}
	}
}

type tapBox struct {
	t   *testing.T
	env *cliEnv
	mux *http.ServeMux
}

func (b *tapBox) do(method, path string, form url.Values) (int, string, string) {
	r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	b.mux.ServeHTTP(w, r)
	return w.Code, w.Header().Get("Location"), w.Body.String()
}

// tapCase: one tap. setup prepares the box and returns the form path and
// values; want is the end text the page and its live fragment show.
type tapCase struct {
	route, name string
	setup       func(b *tapBox) (path string, form url.Values, release func())
	want        string
}

var tapCases = []tapCase{
	{route: "POST /inbox/{id}/{decision}", name: "Freigeben ends fertig", want: "fertig",
		setup: func(b *tapBox) (string, url.Values, func()) {
			p := proposeService(b.t, b.env, "w1")
			return fmt.Sprintf("/inbox/%d/approve", p.ID), nil, nil
		}},
	{route: "POST /inbox/{id}/{decision}", name: "Ablehnen ends in an abgelehnt card", want: "abgelehnt",
		setup: func(b *tapBox) (string, url.Values, func()) {
			p := proposeService(b.t, b.env, "w1")
			return fmt.Sprintf("/inbox/%d/reject", p.ID), url.Values{"grund": {"nicht jetzt"}}, nil
		}},
	{route: "POST /inbox/{id}/{decision}", name: "Zurücknehmen waits for the work before it", want: "zurückgenommen",
		setup: func(b *tapBox) (string, url.Values, func()) {
			p := proposeService(b.t, b.env, "w1")
			if code := inbox.Approve(context.Background(), &cliEnv{Paths: b.env.Paths, Actor: "human:test"}, []string{fmt.Sprint(p.ID)}); code != cli.ExitOK {
				b.t.Fatalf("approve: exit %d", code)
			}
			st, _ := cli.LoadState(b.env)
			unlock, _ := apply.TakeLock(b.env, st, "converge") // a converge tick holds the lock
			return fmt.Sprintf("/inbox/%d/rollback", p.ID), nil, unlock
		}},
	{route: "POST /inbox/{id}/{decision}", name: "a failed Zurücknehmen stays in the live block with its reason", want: "zurücknehmen",
		setup: func(b *tapBox) (string, url.Values, func()) {
			p := proposeService(b.t, b.env, "never")
			return fmt.Sprintf("/inbox/%d/rollback", p.ID), nil, nil // never approved: nothing to take back
		}},
	{route: "POST /inbox/{id}/{decision}", name: "a proposal that is not there", want: "gibt es nicht",
		setup: func(b *tapBox) (string, url.Values, func()) { return "/inbox/99/approve", nil, nil }},
	{route: "/inbox/alle", name: "Alle freigeben ends fertig", want: "fertig",
		setup: func(b *tapBox) (string, url.Values, func()) {
			p, q := proposeService(b.t, b.env, "w1"), proposeService(b.t, b.env, "w2")
			return "/inbox/alle", url.Values{"id": {fmt.Sprint(p.ID), fmt.Sprint(q.ID)}}, nil
		}},
	{route: "POST /check/{service}", name: "jetzt prüfen", want: `whoami`,
		setup: func(b *tapBox) (string, url.Values, func()) {
			withCheck(b.t, b.env)
			return "/check/whoami", nil, nil
		}},
	{route: "POST /check/{service}", name: "a service without a check", want: "keine Prüfung",
		setup: func(b *tapBox) (string, url.Values, func()) { return "/check/caddy", nil, nil }},
	{route: "/schluessel", name: "a key is stored", want: "fertig",
		setup: func(b *tapBox) (string, url.Values, func()) {
			withOwnerKey(b.t, b.env)
			return "/schluessel", url.Values{"name": {"WHOAMI_KEY"}, "wert": {"geheim-854"}}, nil
		}},
	{route: "/schluessel", name: "an empty key", want: "Kein Schlüssel eingegeben",
		setup: func(b *tapBox) (string, url.Values, func()) {
			withOwnerKey(b.t, b.env)
			return "/schluessel", url.Values{"name": {"WHOAMI_KEY"}, "wert": {" "}}, nil
		}},
}

func withCheck(t *testing.T, env *cliEnv) {
	svc := filepath.Join(env.Paths.State, "services", "whoami")
	b, _ := os.ReadFile(filepath.Join(svc, "laib.yml"))
	os.WriteFile(filepath.Join(svc, "laib.yml"), append(b, []byte("status:\n  file: ${DATA_DIR}/whoami/laib-status.json\n  check: whoami-check.service\n")...), 0o644)
	os.WriteFile(filepath.Join(svc, "whoami-check.service"), []byte("[Service]\nType=oneshot\nExecStart=/bin/true\n"), 0o644)
}

func withOwnerKey(t *testing.T, env *cliEnv) {
	svc := filepath.Join(env.Paths.State, "services", "whoami")
	b, _ := os.ReadFile(filepath.Join(svc, "laib.yml"))
	os.WriteFile(filepath.Join(svc, "laib.yml"), bytes.Replace(b, []byte("secrets:\n"), []byte("secrets:\n  - name: WHOAMI_KEY\n    external: true\n    optional: true\n    owner: {label: \"Whoami: Testschlüssel\", warum: \"Damit grüßt whoami mit Namen.\", hilfe: \"Bei whoami.example.\"}\n"), 1), 0o644)
}

// fragmentOf is where the live script fetches a page's running blocks.
func fragmentOf(loc string) string {
	u, _ := url.Parse(loc)
	return strings.TrimSuffix(u.Path, "/") + "/stand"
}

func TestEveryTapEndsVisiblyWhereItBegan(t *testing.T) {
	for _, c := range tapCases {
		t.Run(c.name, func(t *testing.T) {
			env, _ := testBox(t)
			b := &tapBox{t: t, env: env, mux: pages.Routes(env, func(h http.HandlerFunc) http.HandlerFunc { return h })}
			path, form, release := c.setup(b)
			code, loc, body := b.do("POST", path, form)
			if release != nil {
				time.AfterFunc(300*time.Millisecond, release)
			}
			if code == http.StatusSeeOther {
				_, _, body = b.do("GET", loc, nil)
			} else {
				loc = ""
			}
			plainEnough(t, path+" → "+loc, body)
			last := body
			if strings.Contains(body, " data-run>") {
				if !strings.Contains(body, "class=frist") {
					t.Errorf("waiting without a stated limit:\n%s", visibleText(body))
				}
				// the live script's loop: the fragment until nothing runs
				until(t, "the tap ends", func() bool {
					_, _, last = b.do("GET", fragmentOf(loc), nil)
					plainEnough(t, fragmentOf(loc), last)
					if strings.Contains(last, " data-run>") && !strings.Contains(last, "class=frist") {
						t.Fatalf("the live block waits without a stated limit:\n%s", last)
					}
					return !strings.Contains(last, " data-run>")
				})
				if !strings.Contains(visibleText(last), c.want) {
					t.Errorf("the live block ends without %q, where the tap began:\n%s", c.want, visibleText(last))
				}
				_, _, body = b.do("GET", loc, nil)
				plainEnough(t, loc, body)
			}
			if !strings.Contains(visibleText(body), c.want) {
				t.Errorf("the page ends without %q:\n%s", c.want, visibleText(body))
			}
		})
	}
}

// #854 (2): the live script gives up visibly: a non-OK answer or polls
// that fail say „Keine Verbindung zur Box“ with the time of the last
// state, and a wait stops at its limit and says so. Never silent, never
// forever.
func TestTheLiveScriptSaysWhenItLosesTheBox(t *testing.T) {
	env, _ := testBox(t)
	st, _ := cli.LoadState(env)
	unlock, _ := apply.TakeLock(env, st, "apply")
	p := proposeService(t, env, "w1")
	defer func() { unlock(); until(t, "carried", settled(env, p.ID)) }() // no run outlives the test
	do := inboxMux(env)
	do("POST", fmt.Sprintf("/inbox/%d/approve", p.ID), nil)
	body := do("GET", "/inbox", nil)
	script := body[strings.Index(body, "<script>"):]
	for _, w := range []string{"Keine Verbindung zur Box", "Stand von", "länger als"} {
		if !strings.Contains(script, w) {
			t.Errorf("the live script lacks %q:\n%s", w, script)
		}
	}
	if regexp.MustCompile(`if\(!r\.ok\)return[;}]`).MatchString(script) {
		t.Errorf("a non-OK answer stops the live script silently:\n%s", script)
	}
}

// #854 (4): Ablehnen's button says it runs, like Freigeben's.
func TestTheRejectButtonSaysItRuns(t *testing.T) {
	env, _ := testBox(t)
	proposeService(t, env, "w1")
	body := inboxMux(env)("GET", "/inbox", nil)
	i := strings.Index(body, "/reject")
	if i < 0 {
		t.Fatalf("no reject form:\n%s", body)
	}
	if form := body[i:]; !strings.Contains(form[:strings.Index(form, "</form>")], "wird abgelehnt") {
		t.Errorf("the reject button does not say it runs: %s", form[:strings.Index(form, "</form>")])
	}
}

// #854 (6): an approve at the shell says in one line that it waits for
// the work before it, instead of hanging silently.
func TestTheShellApproveSaysItWaits(t *testing.T) {
	env, _ := testBox(t)
	st, _ := cli.LoadState(env)
	unlock, _ := apply.TakeLock(env, st, "converge")
	p := proposeService(t, env, "w1")
	var out bytes.Buffer
	time.AfterFunc(1500*time.Millisecond, unlock)
	shell := &cliEnv{Paths: env.Paths, Actor: "human:test", Out: &out, ErrOut: &out, Wait: time.Minute}
	if code := inbox.Approve(context.Background(), shell, []string{fmt.Sprint(p.ID)}); code != cli.ExitOK {
		t.Fatalf("approve: exit %d\n%s", code, out.String())
	}
	if n := strings.Count(out.String(), "wartet, bis die Box"); n != 1 {
		t.Errorf("the shell approve said %d times that it waits, want once:\n%s", n, out.String())
	}
	if got, _ := proposal.Get(cli.InboxDir(env), p.ID); got.Outcome == "waiting" {
		t.Errorf("not applied after the wait: %+v", got)
	}
}
