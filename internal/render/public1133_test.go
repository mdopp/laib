package render

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/state"
)

// laib#1133, owner decision J: files.<domain> is only at home, its share
// links are for everyone. The golden case perperson gives leser (an internal
// route) publicPaths: /share/ and /api/public/.

func publicSite1133(t *testing.T) string {
	t.Helper()
	st, err := state.Load("../../testdata/golden/perperson/state")
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	return regexp.MustCompile(`(?ms)^leser\.example\.home \{\n.*?^\}$`).FindString(out["caddy/Caddyfile"])
}

// The public paths come before the LAN's handle and without Authelia; the
// rest of the site is unchanged, behind @lan and the login.
func TestPublicPathsBeforeTheLANAndWithoutLogin(t *testing.T) {
	site := publicSite1133(t)
	pub := strings.Index(site, "\t@public path /share/* /api/public/*\n\thandle @public {\n\t\treverse_proxy 127.0.0.1:8490\n\t}\n")
	lan := strings.Index(site, "\t@lan remote_ip private_ranges\n\thandle @lan {\n\t\timport authelia\n")
	if pub < 0 || lan < 0 || pub > lan || !strings.Contains(site, "\timport lanonly\n") {
		t.Fatalf("want the public paths first, then the LAN behind Authelia, then lanonly:\n%s", site)
	}
	st, _ := state.Load("../../testdata/golden/perperson/state")
	for _, n := range Net(st).Names {
		if n.Name == "leser.example.home" && n.Reach != "nur LAN; aus dem Internet: /share/ /api/public/" {
			t.Fatalf("laib net says %q", n.Reach)
		}
	}
}

// publicPaths only where they mean something: path prefixes on an internal
// route; a public one has bypass for paths without a login.
func TestPublicPathsOnlyOnAnInternalRoute(t *testing.T) {
	for _, bad := range []string{
		"    exposure: public\n    auth: one_factor\n    publicPaths: [/share/]",
		"    exposure: internal\n    auth: one_factor\n    publicPaths: [/]",
		"    exposure: internal\n    auth: one_factor\n    publicPaths: [share/]",
	} {
		dir := t.TempDir()
		copyTree(t, "../../testdata/golden/perperson/state", dir)
		f := filepath.Join(dir, "services", "leser", "laib.yml")
		b, _ := os.ReadFile(f)
		s := strings.Replace(string(b), "    exposure: internal\n    auth: one_factor\n    publicPaths: [/share/, /api/public/]", bad, 1)
		if s == string(b) {
			t.Fatal("the golden leser route changed: adapt the test")
		}
		os.WriteFile(f, []byte(s), 0o644)
		if _, err := state.Load(dir); err == nil || !strings.Contains(err.Error(), "publicPaths") {
			t.Errorf("%q loads: %v", bad, err)
		}
	}
}

// With LAIB_CADDY naming a caddy binary, the site runs in Caddy itself,
// the test's own address counted as outside the LAN: only the public
// paths reach the app, and no path that leaves them through a `..`, an
// escaped one or a doubled slash does.
func TestPublicPathsInCaddyItself(t *testing.T) {
	bin := os.Getenv("LAIB_CADDY")
	if bin == "" {
		t.Skip("LAIB_CADDY names no caddy binary")
	}
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "app "+r.URL.Path)
	}))
	defer app.Close()
	site := publicSite1133(t)
	site = strings.ReplaceAll(site, "127.0.0.1:8490", strings.TrimPrefix(app.URL, "http://"))
	site = strings.Replace(site, "remote_ip private_ranges", "remote_ip 192.0.2.0/24", 1) // the test's 127.0.0.1 is outside
	site = strings.Replace(site, "\t\timport authelia\n", "", 1)
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	caddyfile := "{\n\tadmin off\n\tauto_https off\n\torder request_header first\n}\n\n(down) {\n}\n\n(lanonly) {\n\thandle {\n\t\trespond \"Nur zu Hause\" 403\n\t}\n}\n\n" +
		strings.Replace(site, "leser.example.home {", "http://leser.example.home:"+strings.Split(addr, ":")[1]+" {", 1) + "\n"
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
	for path, want := range map[string]string{
		"/share/abc123":                    "app /share/abc123",
		"/api/public/dl/abc123/a.pdf":      "app /api/public/dl/abc123/a.pdf",
		"/":                                "403",
		"/files/":                          "403",
		"/api/resources/":                  "403",
		"/api/public":                      "403",
		"/share/../api/resources/":         "403",
		"/share/%2e%2e/api/resources/":     "403",
		"/share/%2E%2E%2Fapi/resources/":   "403",
		"//api/public/../../api/resources": "403",
		"/api/public/%2e%2e/login":         "403",
	} {
		req, _ := http.NewRequest("GET", "http://"+addr+"/", nil)
		req.URL.Opaque = path // as sent, not cleaned by Go
		req.Host = "leser.example.home"
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		got := fmt.Sprint(resp.StatusCode)
		if resp.StatusCode == 200 {
			got = string(body)
		}
		if strings.HasPrefix(got, "app ") && want == "403" {
			t.Errorf("%s from outside reached the app as %q", path, got)
		} else if got != want && !(want == "403" && !strings.HasPrefix(got, "app ")) {
			t.Errorf("%s from outside: got %q, want %q", path, got, want)
		}
	}
}
