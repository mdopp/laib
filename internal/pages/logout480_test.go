package pages

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/cli"
)

// #480: the box page has a way out: "Abmelden" on every page leads to
// Authelia's logout, and back to the box page's login afterwards, so the
// admin can sign in with another account. The link stays on the page's own
// host; /abmelden knows where Authelia lives.
func TestTheBoxPageCanLogOut(t *testing.T) {
	body := statusBody(Readiness{State: "bereit"}, nil, nil, "", [2]string{}, false)
	for _, page := range []string{body, nav} {
		if !strings.Contains(page, `<a href="/abmelden">Abmelden</a>`) {
			t.Errorf("no Abmelden link in %s", page)
		}
	}
	env := &cli.Env{Paths: boxfs.Paths{State: "../../testdata/golden/nuc/state"}}
	w := httptest.NewRecorder()
	Abmelden(env)(w, httptest.NewRequest("GET", "/abmelden", nil))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "https://auth.nuc.home/logout?rd=https%3A%2F%2Flaib.nuc.home%2F" {
		t.Fatalf("/abmelden: %d %q", w.Code, w.Header().Get("Location"))
	}
}
