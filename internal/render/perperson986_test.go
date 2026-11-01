package render

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/state"
)

// #986, ADR 0029: the persons' instances of one template share one name.
// The golden case perperson has martin's and anna's notizbuch behind
// notizen.<domain>, and lena signed in with none of her own.

func perPerson986(t *testing.T, secrets map[string]string) (*state.State, Output) {
	t.Helper()
	st, err := state.Load("../../testdata/golden/perperson/state")
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render(st, secrets)
	if err != nil {
		t.Fatal(err)
	}
	return st, out
}

// a request to the shared name: its path as sent, who has a session with
// Authelia, and a Remote-User the client sends itself
type ask986 struct{ path, session, sent string }

// cases986: what each request must reach, from the state, not the text: a
// person's own instance with its key, an agent's path to that instance
// without a key, anyone else denied. The last one is the forged header on
// a path Authelia lets through and Caddy cleans to another (/p/anna/../):
// only the strip keeps it from martin's archive with martin's key.
func cases986(st *state.State) map[ask986]string {
	port := map[string]int{}
	for _, i := range st.Instances("notizen") {
		port[i.Service.Contract.Person] = i.Route.Port
	}
	key := func(p string) string {
		return fmt.Sprintf("%d %s", port[p], Placeholder("NOTIZBUCH_KEY_"+strings.ToUpper(p)))
	}
	return map[ask986]string{
		{"/", "anna", ""}:                         key("anna"),
		{"/suche", "anna", "martin"}:              key("anna"),
		{"/", "martin", "anna"}:                   key("martin"),
		{"/", "lena", ""}:                         "denied",
		{"/", "lena", "martin"}:                   "denied",
		{"/", "", "martin"}:                       "login",
		{"/p/martin/api/search", "", ""}:          fmt.Sprint(port["martin"]),
		{"/p/martin/api/search", "anna", "anna"}:  fmt.Sprint(port["martin"]),
		{"/p/anna/../api/search", "", "martin"}:   "denied",
		{"/p/anna/../../suche", "lena", "martin"}: "denied",
	}
}

// site986 is the rendered notizen site and its Authelia bypass rules.
func site986(out Output) (string, []*regexp.Regexp) {
	site := regexp.MustCompile(`(?ms)^notizen\.example\.home \{\n.*?^\}$`).FindString(out["caddy/Caddyfile"])
	var bypass []*regexp.Regexp
	for _, m := range regexp.MustCompile(`(?m)^    - domain: notizen\.example\.home\n      policy: bypass\n      resources: \["(.*)"\]$`).FindAllStringSubmatch(out["authelia/laib-access.yml"], -1) {
		bypass = append(bypass, regexp.MustCompile(m[1]))
	}
	return site, bypass
}

// serve986 runs a request through the site as Caddy does with what Laib
// writes there: request_header first (order request_header first),
// forward_auth before any handle (Authelia: a bypass rule passes with no
// user and copies none, else the session's user or the login), then the
// handles, path ones on the cleaned path first, the Remote-User ones, the
// catch-all last.
func serve986(site string, bypass []*regexp.Regexp, a ask986) string {
	user := ""
	if !strings.Contains(site, "\n\trequest_header -Remote-*\n") {
		user = a.sent
	}
	passed := false
	for _, re := range bypass {
		passed = passed || re.MatchString(a.path)
	}
	switch {
	case passed:
	case !strings.Contains(site, "import authelia"):
		return "no login on the site"
	case a.session == "":
		return "login"
	default:
		user = a.session
	}
	clean := path.Clean(a.path)
	for _, m := range regexp.MustCompile(`handle (/p/[a-z-]+/)\* \{\n\s*uri strip_prefix /p/[a-z-]+\n\s*reverse_proxy 127\.0\.0\.1:(\d+)\n`).FindAllStringSubmatch(site, -1) {
		if strings.HasPrefix(clean+"/", m[1]) {
			return m[2]
		}
	}
	for _, m := range regexp.MustCompile(`@(person_[a-z-]+) header Remote-User ([a-z-]+)\n\s*handle @(person_[a-z-]+) \{\n\s*reverse_proxy 127\.0\.0\.1:(\d+) \{\n\s*header_up X-API-Key "(.*)"`).FindAllStringSubmatch(site, -1) {
		if m[1] == m[3] && user == m[2] {
			return m[4] + " " + m[5]
		}
	}
	if strings.Contains(site, "handle {\n\t\t\timport denied") {
		return "denied"
	}
	return "nothing"
}

func TestOneNameHandsEachPersonTheirOwnInstance(t *testing.T) {
	st, out := perPerson986(t, nil)
	site, bypass := site986(out)
	if site == "" || len(bypass) != 2 {
		t.Fatalf("want one notizen site and a bypass per person, got %d rules:\n%s", len(bypass), out["caddy/Caddyfile"])
	}
	if n := strings.Count(out["caddy/Caddyfile"], "notizen.example.home {"); n != 1 {
		t.Fatalf("two instances, one name: want one site, got %d", n)
	}
	check := func(site string) (bad []string) {
		for a, want := range cases986(st) {
			if got := serve986(site, bypass, a); got != want {
				bad = append(bad, fmt.Sprintf("%+v: got %q, want %q", a, got, want))
			}
		}
		return bad
	}
	if bad := check(site); len(bad) > 0 {
		t.Fatalf("the shared name hands a request to the wrong place:\n%s\n%s", strings.Join(bad, "\n"), site)
	}
	// the check can go red: without the strip, a forged Remote-User on a
	// path Authelia lets through reaches martin's archive with his key; with
	// the dispatch on the wrong header value, anna gets martin's
	for what, broken := range map[string]string{
		"no strip":       strings.Replace(site, "\trequest_header -Remote-*\n", "", 1),
		"wrong dispatch": strings.Replace(site, "@person_anna header Remote-User anna", "@person_anna header Remote-User martin", 1),
		"no denied":      strings.Replace(site, "import denied", "reverse_proxy 127.0.0.1:8480", 1),
	} {
		if len(check(broken)) == 0 {
			t.Errorf("%s: the check stays green on a broken site", what)
		}
	}
}

// laib net and laib-app check see the one name, with each person's path
func TestTheSharedNameIsOneNameOnTheMap(t *testing.T) {
	st, _ := perPerson986(t, nil)
	var names []NetName
	for _, n := range Net(st).Names {
		if n.Name == "notizen.example.home" {
			names = append(names, n)
		}
	}
	if len(names) != 1 || strings.Join(names[0].Bypass, " ") != "/p/anna/ /p/martin/" || !strings.HasPrefix(names[0].Upstream, "notizbuch je Person: anna 127.0.0.1:8481, martin 127.0.0.1:8480") {
		t.Fatalf("want one name with both persons' paths, got %+v", names)
	}
}

// With LAIB_CADDY naming a caddy binary, the rendered site runs in Caddy
// itself, against a stand-in Authelia and the two instances: the same
// cases as above, now with Caddy's own directive order, path cleaning and
// forward_auth. Skipped without it (CI validates the golden Caddyfiles).
func TestOneNameInCaddyItself(t *testing.T) {
	bin := os.Getenv("LAIB_CADDY")
	if bin == "" {
		t.Skip("LAIB_CADDY names no caddy binary")
	}
	secrets := map[string]string{"NOTIZBUCH_KEY_ANNA": "schluessel-anna", "NOTIZBUCH_KEY_MARTIN": "schluessel-martin"}
	st, out := perPerson986(t, secrets)
	site, bypass := site986(out)
	authelia := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, re := range bypass {
			if re.MatchString(r.Header.Get("X-Forwarded-Uri")) {
				return
			}
		}
		if c, err := r.Cookie("session"); err == nil {
			w.Header().Set("Remote-User", c.Value)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer authelia.Close()
	caddyfile := "{\n\tadmin off\n\tauto_https off\n\torder request_header first\n}\n\n"
	for _, s := range regexp.MustCompile(`(?ms)^\((lanonly|denied|down|authelia)\) \{\n.*?^\}$`).FindAllString(out["caddy/Caddyfile"], -1) {
		caddyfile += s + "\n\n"
	}
	caddyfile = strings.Replace(caddyfile, "forward_auth 127.0.0.1:9091", "forward_auth "+strings.TrimPrefix(authelia.URL, "http://"), 1)
	for _, i := range st.Instances("notizen") {
		p := i.Service.Contract.Person
		inst := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "%s %s", p, r.Header.Get("X-API-Key"))
		}))
		defer inst.Close()
		site = strings.ReplaceAll(site, fmt.Sprintf("127.0.0.1:%d\n", i.Route.Port), strings.TrimPrefix(inst.URL, "http://")+"\n")
		site = strings.ReplaceAll(site, fmt.Sprintf("127.0.0.1:%d {", i.Route.Port), strings.TrimPrefix(inst.URL, "http://")+" {")
	}
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	caddyfile += strings.Replace(site, "notizen.example.home {", "http://notizen.example.home:"+strings.Split(addr, ":")[1]+" {", 1) + "\n"
	f := filepath.Join(t.TempDir(), "Caddyfile")
	os.WriteFile(f, []byte(caddyfile), 0o644)
	cmd := exec.Command(bin, "run", "--config", f, "--adapter", "caddyfile")
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for a, want := range cases986(st) {
		req, _ := http.NewRequest("GET", "http://"+addr+a.path, nil)
		req.Host = "notizen.example.home"
		if a.session != "" {
			req.AddCookie(&http.Cookie{Name: "session", Value: a.session})
		}
		if a.sent != "" {
			req.Header.Set("Remote-User", a.sent)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		got := map[int]string{401: "login", 403: "denied"}[resp.StatusCode]
		if resp.StatusCode == 200 {
			got = string(body)
		}
		// the instance answers with its person and the key it was sent
		for p, k := range map[string]string{"anna": "schluessel-anna", "martin": "schluessel-martin"} {
			for _, i := range st.Instances("notizen") {
				if i.Service.Contract.Person == p {
					want = strings.Replace(want, fmt.Sprintf("%d %s", i.Route.Port, Placeholder("NOTIZBUCH_KEY_"+strings.ToUpper(p))), p+" "+k, 1)
					if want == fmt.Sprint(i.Route.Port) {
						want = p + " "
					}
				}
			}
		}
		if got != want {
			t.Errorf("%+v: Caddy answered %d %q, want %q", a, resp.StatusCode, body, want)
		}
	}
}
