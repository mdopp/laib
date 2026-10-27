package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/pages"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/secrets"
)

// #309: the Box page shows what a template says about itself with its age;
// looking at a stale one starts the template's check in the background and
// the page reloads until it lands; "jetzt prüfen" starts it on demand.
func TestTheBoxPageShowsAndRefreshesATemplatesStatus(t *testing.T) {
	c := newCbox(t)
	svc := filepath.Join(c.env.Paths.State, "services", "whoami")
	b, _ := os.ReadFile(filepath.Join(svc, "laib.yml"))
	os.WriteFile(filepath.Join(svc, "laib.yml"), append(b, []byte("status:\n  file: ${DATA_DIR}/whoami/laib-status.json\n  check: whoami-check.service\n")...), 0o644)
	os.WriteFile(filepath.Join(svc, "whoami-check.service"), []byte("[Service]\nType=oneshot\nExecStart=/bin/true\n"), 0o644)
	data := filepath.Join(filepath.Dir(c.env.Paths.State), "data", "whoami")
	os.MkdirAll(data, 0o755)
	at := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	os.WriteFile(filepath.Join(data, "laib-status.json"), []byte(`{"at":"`+at+`","detail":"fehlt: CalDAV"}`), 0o644)

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
	do := func(method, path string) (*http.Response, error) { // through Caddy, as Authelia's user sees it
		req, _ := http.NewRequest(method, "http://"+addr+path, nil)
		req.Header.Set("X-Laib-Proxy", sec[render.ProxySecret])
		return http.DefaultTransport.RoundTrip(req)
	}
	var page string
	for i := 0; i < 50; i++ {
		if resp, err := do("GET", "/"); err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			page = string(body)
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(page, "fehlt: CalDAV · geprüft vor 2 h") {
		t.Fatalf("the page shows the status with its age:\n%s", page)
	}
	if !strings.Contains(page, "wird geprüft") || !strings.Contains(page, "data-run>") || !strings.Contains(page, "fetch(") || strings.Contains(page, "http-equiv") {
		t.Fatalf("a stale status is being checked and only its card comes again, not the page:\n%s", page)
	}
	// the fragment: the service cards alone, behind the same guard, GET only
	if resp, err := do("GET", "/stand"); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("the fragment: %v %v", err, resp)
	} else {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if part := string(body); !strings.HasPrefix(part, `<div class="card `) || !strings.Contains(part, `id="svc-whoami"`) || strings.Contains(part, "<html") || strings.Contains(part, "<h1") {
			t.Fatalf("the fragment is not the service cards alone:\n%s", part)
		}
	}
	for _, path := range []string{"/stand", "/inbox/stand", "/schluessel/stand"} {
		req, _ := http.NewRequest("GET", "http://"+addr+path, nil)
		if resp, err := http.DefaultTransport.RoundTrip(req); err != nil || resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s without the proxy secret: %v %v, want 403", path, err, resp)
		}
	}
	if resp, _ := do("POST", "/stand"); resp == nil || resp.StatusCode == http.StatusOK {
		t.Errorf("the fragment takes a POST: %v", resp)
	}
	started := func() int {
		return strings.Count(strings.Join(c.calls, "\n"), "systemctl --user start --no-block whoami-check.service")
	}
	if started() != 1 {
		t.Fatalf("looking at a stale status starts the check once: %v", c.calls)
	}
	resp, err := do("POST", "/check/whoami")
	if err != nil || resp.StatusCode != http.StatusSeeOther || started() != 2 {
		t.Fatalf("jetzt prüfen starts the check and goes back to the page: %v %v %v", err, resp, c.calls)
	}
	if resp, _ := do("POST", "/check/caddy"); resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a service without a check has nothing to start: %v", resp)
	}
	// no file yet: the page says so instead of showing nothing, and starts the check
	os.Remove(filepath.Join(data, "laib-status.json"))
	resp, err = do("GET", "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "noch kein Stand") || started() != 3 {
		t.Fatalf("a declared status not written yet is a line, and its check starts:\n%s\n%v", body, c.calls)
	}
	st, _ := cli.LoadState(c.env)
	if _, rows := pages.CollectStatus(context.Background(), c.env, st); !slices.ContainsFunc(rows, func(r pages.ServiceStatus) bool {
		return r.Name == "whoami" && r.Detail == "noch kein Stand"
	}) {
		t.Fatalf("laib status says it too: %+v", rows)
	}
}

// #359: laib status prints what the status file says, not only --json.
func TestLaibStatusPrintsTheTemplatesStatusLine(t *testing.T) {
	c := newCbox(t)
	svc := filepath.Join(c.env.Paths.State, "services", "whoami")
	b, _ := os.ReadFile(filepath.Join(svc, "laib.yml"))
	os.WriteFile(filepath.Join(svc, "laib.yml"), append(b, []byte("status:\n  file: ${DATA_DIR}/whoami/laib-status.json\n")...), 0o644)
	data := filepath.Join(filepath.Dir(c.env.Paths.State), "data", "whoami")
	os.MkdirAll(data, 0o755)
	os.WriteFile(filepath.Join(data, "laib-status.json"), []byte(`{"detail":"Passwort-Mails gehen nicht (kein Mailserver)"}`), 0o644)
	var out bytes.Buffer
	c.env.Out = &out
	if rc := pages.Status(context.Background(), c.env, nil); rc != cli.ExitOK {
		t.Fatalf("status rc %d", rc)
	}
	line := ""
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.Contains(l, " whoami ") {
			line = l
		}
	}
	if !strings.Contains(line, "Passwort-Mails gehen nicht (kein Mailserver)") {
		t.Fatalf("the whoami line carries the status detail:\n%s", out.String())
	}
}

// #688: laib_status for one named service carries its status file's own
// fields (Haus shows one upload's result); the whole box's answer does not.
func TestStatusForOneServiceCarriesTheStatusFilesOwnFields(t *testing.T) {
	c := newCbox(t)
	svc := filepath.Join(c.env.Paths.State, "services", "whoami")
	b, _ := os.ReadFile(filepath.Join(svc, "laib.yml"))
	os.WriteFile(filepath.Join(svc, "laib.yml"), append(b, []byte("status:\n  file: ${DATA_DIR}/whoami/laib-status.json\n")...), 0o644)
	data := filepath.Join(filepath.Dir(c.env.Paths.State), "data", "whoami")
	os.MkdirAll(data, 0o755)
	os.WriteFile(filepath.Join(data, "laib-status.json"), []byte(`{"detail":"Musik-Eingang: bereit","wartet":0,"letzte":[{"eingang":"a.zip","von":"mama"}]}`), 0o644)
	status := func(args ...string) string {
		var buf bytes.Buffer
		if rc := pages.Status(context.Background(), &cliEnv{Paths: c.env.Paths, JSON: true, Out: &buf}, args); rc != cli.ExitOK {
			t.Fatalf("status rc %d", rc)
		}
		return buf.String()
	}
	var one struct {
		Services []struct {
			Eigenes struct {
				Wartet *int
				Letzte []struct{ Eingang, Von string }
			}
		}
	}
	if out := status("whoami"); json.Unmarshal([]byte(out), &one) != nil || len(one.Services) != 1 || one.Services[0].Eigenes.Wartet == nil ||
		len(one.Services[0].Eigenes.Letzte) != 1 || one.Services[0].Eigenes.Letzte[0].Von != "mama" {
		t.Fatalf("one service: its own fields under eigenes:\n%s", out)
	}
	if out := status(); strings.Contains(out, "eigenes") {
		t.Fatalf("the whole box: no service's own fields:\n%s", out)
	}
}
