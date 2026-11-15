package pages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/state"
)

// #1236: every admin task is on Laib's page; a Homes page or the Bote is the
// extra way, offered only when there is a Homes; a key's way is its form.
func TestATaskOnLaibsPageKeepsItsOtherWays(t *testing.T) {
	home := t.TempDir()
	env := &cli.Env{Paths: boxfs.Paths{Home: home, State: home}}
	os.WriteFile(filepath.Join(home, "aufgaben.yml"), []byte(`aufgaben:
  - {id: 1, titel: Stimme einrichten, seit: 2026-11-01, von: claude, weg: {seite: /stimme}}
  - {id: 2, titel: Mit dem Boten, seit: 2026-11-01, von: claude, weg: {gespraech: true}, erinnern: 2026-11-02}
  - {id: 3, titel: Discogs, seit: 2026-11-01, von: claude, weg: {schluessel: HOMES_DISCOGS_KEY}, fertig_wenn: schluessel HOMES_DISCOGS_KEY}
  - {id: 4, titel: Fertig, seit: 2026-11-01, von: claude, erledigt: {am: 2026-11-02, von: human:martin}}
`), 0o644)
	st := &state.State{Services: map[string]*state.Service{}}
	st.Box.Domain, st.Box.Laib.Host = "box.test", "laib"
	without := aufgabenPart(env, st)
	st.Services["homes"] = &state.Service{Name: "homes", Contract: state.Contract{Provides: []string{"homes"}, Routes: []state.Route{{Host: "home"}}}}
	with := aufgabenPart(env, st)
	for _, want := range []string{`<a href="https://home.box.test/stimme">Auch in Homes ›</a>`, "Der Bote führt auch im Gespräch hindurch.", `href="https://laib.box.test/schluessel?name=HOMES_DISCOGS_KEY"`, `class="card gelb" id=aufgabe-2`, "Laib hakt sie selbst ab"} {
		if !strings.Contains(with, want) {
			t.Errorf("with Homes the tasks lack %q:\n%s", want, with)
		}
	}
	if strings.Contains(without, "Auch in Homes") || !strings.Contains(without, "Stimme einrichten") || strings.Contains(with, "Fertig") {
		t.Errorf("without Homes a Homes page's task is on Laib's page without a link, a done one is gone:\n%s", without)
	}
}

// The morning report in words; one that names a command goes under Details (#855).
func TestTheMorningReportIsOnLaibsPage(t *testing.T) {
	home := t.TempDir()
	env := &cli.Env{Paths: boxfs.Paths{Home: home}}
	if reportPart(env) != "" {
		t.Error("no report, yet a section")
	}
	os.MkdirAll(filepath.Join(home, "reports"), 0o755)
	os.WriteFile(filepath.Join(home, "reports", "2026-11-14.md"), []byte("Gestern."), 0o644)
	os.WriteFile(filepath.Join(home, "reports", "2026-11-15.md"), []byte("Bitte laib diag laufen lassen."), 0o644)
	if got := reportPart(env); !strings.Contains(got, "Vom 15.11.2026: er nennt technische Angaben") || !strings.Contains(got, "<details>") || strings.Contains(got, "Gestern") {
		t.Errorf("the latest report, its command under Details: %s", got)
	}
}
