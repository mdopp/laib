package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/pages"
	"github.com/mdopp/laib/internal/policy"
)

// #798: the admin page keeps what approving, keys and repair need. /net,
// /bote/link and the morning report left it (`laib net` and Haus keep
// them), family-card and adopt left the verb table.
func TestTheAdminPageKeepsOnlyApproveKeysAndRepair(t *testing.T) {
	c := newCbox(t)
	os.MkdirAll(filepath.Join(c.env.Paths.Home, "reports"), 0o755)
	os.WriteFile(filepath.Join(c.env.Paths.Home, "reports", "2026-10-27.md"), []byte("Alles ruhig."), 0o644)
	mux := pages.Routes(c.env, func(h http.HandlerFunc) http.HandlerFunc { return h })
	get := func(path string) (int, string) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w.Code, w.Body.String()
	}
	for _, gone := range []string{"/net", "/bote/link"} {
		if _, pattern := mux.Handler(httptest.NewRequest("GET", gone, nil)); pattern != "/" {
			t.Errorf("%s is still a route (%s)", gone, pattern)
		}
		if code, _ := get(gone); code != http.StatusNotFound {
			t.Errorf("%s answers %d, want 404", gone, code)
		}
	}
	for _, kept := range []string{"/", "/inbox", "/journal", "/schluessel"} {
		if code, _ := get(kept); code != http.StatusOK {
			t.Errorf("%s answers %d, want 200", kept, code)
		}
	}
	_, status := get("/")
	for _, w := range []string{"Morgenbericht", "Alles ruhig.", `href="/net"`, "/bote/link"} {
		if strings.Contains(status, w) {
			t.Errorf("the Status page still holds %q", w)
		}
	}
	for _, v := range mcp.Verbs {
		if v.Name == "adopt" || v.Name == "family-card" {
			t.Errorf("the verb table still has %s", v.Name)
		}
	}
	if mcp.Allowed["adopt"] || policy.KnownVerbs["adopt"] {
		t.Error("adopt is still a verb an agent or policy.yml may name")
	}
}

// #798, #469: the steps /bote/link printed live on in the Bote's README,
// so nothing is lost: signal-cli runs as its own daemon without a QR
// endpoint, and the number is registered (a spare landline by voice call,
// no second SIM) or linked to a phone, with the daemon stopped.
func TestTheBoteReadmeTellsHowToGiveTheBoteItsNumber(t *testing.T) {
	b, err := os.ReadFile("../../templates/bote/README.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	for _, w := range []string{"systemctl --user stop bote-signal", "--entrypoint /usr/bin/signal-cli-native", "${DATA_DIR}/bote/signal:/home/.local/share/signal-cli:z",
		"register --voice --captcha", "verify <code>", "setPin <PIN>", "link -n Bote", "podman unshare chown -R 1000:1000 ${DATA_DIR}/bote/signal", "systemctl --user start bote-signal"} {
		if !strings.Contains(doc, w) {
			t.Errorf("the Bote README lacks %q", w)
		}
	}
	if strings.Contains(doc, "Bote verbinden") {
		t.Error("the Bote README still points at the Box page's Bote verbinden")
	}
}
