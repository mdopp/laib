package pages

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/sysd"
)

// 2026-11-14 17:57: home-assistant-drucker.service, a oneshot its timer fires
// every quarter hour, failed; Laib said „Home Assistant läuft nicht“ and sent
// „seit 10 Minuten rot“ while Home Assistant ran. A helper that fails is gelb:
// the service runs, and every surface says which helper and its own last log
// line (not systemd's, not podman's). Its repair stays the one diag offers.
// Ablauf L-52
func TestAFailedHelperIsYellowAndSaysItsLastLine(t *testing.T) {
	env := fixture856(t)
	dir := filepath.Join(env.Paths.State, "services", "usage-metrics")
	for f, body := range map[string]string{
		"usage-metrics-pflege.service": "[Unit]\nDescription=prune\n\n[Service]\nType=oneshot\nExecStart=/bin/true\n",
		"usage-metrics-pflege.timer":   "[Timer]\nOnCalendar=*:0/15\n\n[Install]\nWantedBy=timers.target\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	real := sysd.Run
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		switch {
		case name == "systemctl" && slices.Contains(args, "is-active") && slices.Contains(args, "usage-metrics-pflege.service"):
			return "failed", nil
		case name == "journalctl" && slices.Contains(args, "usage-metrics-pflege.service"):
			return "2026-11-14T16:57:00+00:00 box systemd[1373]: Starting usage-metrics-pflege.service - prune...\n" +
				"2026-11-14T16:57:01+00:00 box sh[42]: pflege: database is locked\n" +
				"2026-11-14T16:57:01+00:00 box podman[43]: container exec_died 1e8c (image=x)\n" +
				"2026-11-14T16:57:01+00:00 box systemd[1373]: usage-metrics-pflege.service: Failed with result 'exit-code'.\n", nil
		}
		return real(ctx, name, args...)
	}
	const say = "Nutzungszähler läuft; ein Helfer meldet einen Fehler: usage-metrics-pflege.service: „pflege: database is locked“"

	var out bytes.Buffer
	if code := diag.Cmd(context.Background(), &cli.Env{Paths: env.Paths, JSON: true, Out: &out}, nil); code != cli.ExitOK {
		t.Fatalf("laib diag: exit %d", code)
	}
	var found []diag.Finding
	if err := json.Unmarshal(out.Bytes(), &found); err != nil {
		t.Fatal(err)
	}
	var unit []diag.Finding
	for _, f := range found {
		if f.Service == "usage-metrics" && f.Probe == "unit" {
			unit = append(unit, f)
		}
	}
	if len(unit) != 1 || unit[0].Severity != "gelb" || unit[0].Say != say || unit[0].Helfer != say {
		t.Fatalf("a failed helper is one gelb finding that says the service runs and the helper's own last line: %+v", unit)
	}
	if !slices.Equal(unit[0].Repair, []string{"restart", "usage-metrics"}) || unit[0].Fix != "laib logs usage-metrics" {
		t.Errorf("the finding keeps its fix and repair verbs: %+v", unit[0])
	}

	var st bytes.Buffer
	if code := Status(context.Background(), &cli.Env{Paths: env.Paths, JSON: true, Out: &st}, nil); code != cli.ExitOK {
		t.Fatalf("laib status --json: exit %d", code)
	}
	var status struct {
		Ready    Readiness       `json:"ready"`
		Services []ServiceStatus `json:"services"`
	}
	json.Unmarshal(st.Bytes(), &status)
	i := slices.IndexFunc(status.Services, func(s ServiceStatus) bool { return s.Name == "usage-metrics" })
	if i < 0 || status.Services[i].State != "gelb" || status.Services[i].Helfer != say || status.Ready.Red != 1 || status.Ready.Yellow != 1 {
		t.Fatalf("the row is gelb with the helper's sentence and only whoami is red: %+v", status)
	}

	mux := Routes(env, func(h http.HandlerFunc) http.HandlerFunc { return h })
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	page := html.UnescapeString(rec.Body.String())
	if !strings.Contains(page, `class="card gelb" id="svc-usage-metrics"`) || !strings.Contains(page, say) || strings.Contains(page, "Nutzungszähler läuft nicht") {
		t.Errorf("the Status page shows the helper's error on a yellow card:\n%s", page)
	}
	var text bytes.Buffer
	Status(context.Background(), &cli.Env{Paths: env.Paths, Out: &text}, nil)
	if !strings.Contains(text.String(), "gelb  usage-metrics") || !strings.Contains(text.String(), say) {
		t.Errorf("laib status names the helper:\n%s", text.String())
	}
}
