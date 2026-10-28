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
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/sysd"
)

// fixture856 is one box: the golden state, every unit active, whoami's
// health probe answering 502, the last green backup 50 h old on a box
// installed ten days ago, and one approved proposal.
func fixture856(t *testing.T) *cli.Env {
	t.Helper()
	home := t.TempDir()
	paths := boxfs.Paths{Home: home, State: filepath.Join(home, "state"), Journal: filepath.Join(home, "journal"),
		Render: filepath.Join(home, "render"), Quadlet: filepath.Join(home, "q"), Systemd: filepath.Join(home, "s"),
		Key: filepath.Join(home, "age.key"), Secrets: filepath.Join(home, "state", "secrets.age"), Lock: filepath.Join(home, "apply.lock")}
	if err := os.CopyFS(paths.State, os.DirFS("../../testdata/golden/basic/state")); err != nil {
		t.Fatal(err)
	}
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	t.Cleanup(good.Close)
	t.Cleanup(bad.Close)
	probe := regexp.MustCompile(`http: http://127\.0\.0\.1:\d+\S*`)
	for _, svc := range []string{"auth", "caddy", "usage-metrics", "whoami"} {
		f := filepath.Join(paths.State, "services", svc, "laib.yml")
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		url := good.URL
		if svc == "whoami" {
			url = bad.URL
		}
		os.WriteFile(f, probe.ReplaceAll(b, []byte("http: "+url+"/")), 0o644)
	}
	// no DNS lookup from a test: the challenge alias is diag's, not this fixture's
	box := filepath.Join(paths.State, "box.yml")
	b, _ := os.ReadFile(box)
	os.WriteFile(box, regexp.MustCompile(`(?m)^\s*challengeAlias:.*$`).ReplaceAll(b, nil), 0o644)

	real := sysd.Run
	t.Cleanup(func() { sysd.Run = real })
	sysd.Run = func(_ context.Context, name string, args ...string) (string, error) {
		if name == "systemctl" && len(args) > 1 && args[1] == "is-active" {
			return "active", nil
		}
		return "", nil
	}
	now := time.Now()
	for _, e := range []journal.Entry{
		{T: now.Add(-10 * 24 * time.Hour), Actor: "laib", Verb: "init", Result: "green"},
		{T: now.Add(-50 * time.Hour), Actor: "laib", Verb: "backup", Result: "green"},
		{T: now.Add(-time.Minute), Actor: "human:mdopp", Verb: "restart", Service: "caddy", Result: "green", Reason: "von Hand neu gestartet"},
	} {
		journal.Append(paths.Journal, e)
	}
	env := &cli.Env{Paths: paths}
	os.MkdirAll(cli.InboxDir(env), 0o755)
	if err := proposal.Save(cli.InboxDir(env), &proposal.Proposal{ID: 7, Actor: "claude", Reason: "whoami neu", Status: "approved",
		Files: []string{"services/whoami/values.yml"}, Created: now.Add(-3 * time.Hour), Decided: now.Add(-2 * time.Hour), DecidedBy: "human:mdopp"}); err != nil {
		t.Fatal(err)
	}
	return env
}

// #856: one fixture, one verdict. /ready (Solaris, the watchdog), the Status
// page, `laib status --json` (Haus reads it over laib_status) and the text
// of `laib status` say the same about what is red, the backup and the last
// change; and what laib diag mails as red is red on them.
func TestOneFixtureGivesTheSameVerdictOnEverySurface(t *testing.T) {
	env := fixture856(t)
	// laib diag first, as the mail runs it: what it finds red is what the mail says
	var diagOut bytes.Buffer
	if code := diag.Cmd(context.Background(), &cli.Env{Paths: env.Paths, JSON: true, Out: &diagOut}, nil); code != cli.ExitOK {
		t.Fatalf("laib diag: exit %d", code)
	}
	var mailed []diag.Finding
	if err := json.Unmarshal(diagOut.Bytes(), &mailed); err != nil {
		t.Fatal(err)
	}
	mux := Routes(env, func(h http.HandlerFunc) http.HandlerFunc { return h })
	get := func(path string) string {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 200 {
			t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body)
		}
		return rec.Body.String()
	}
	var ready Readiness
	if err := json.Unmarshal([]byte(get("/ready")), &ready); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := Status(context.Background(), &cli.Env{Paths: env.Paths, JSON: true, Out: &out}, nil); code != cli.ExitOK {
		t.Fatalf("laib status --json: exit %d", code)
	}
	var status struct {
		Ready    Readiness       `json:"ready"`
		Services []ServiceStatus `json:"services"`
	}
	if err := json.Unmarshal(out.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	ready.LastHeard, status.Ready.LastHeard = "", ""
	a, _ := json.Marshal(ready)
	b, _ := json.Marshal(status.Ready)
	if !bytes.Equal(a, b) {
		t.Errorf("/ready and laib status --json disagree:\n/ready %s\nstatus %s", a, b)
	}

	// a health probe's red is red, and readiness stays what DESIGN.md says it is
	if ready.State != "bereit" || ready.Red != 1 || ready.Lock != "" {
		t.Errorf("one red health probe: want bereit with red 1 and no lock, got %s red %d lock %q", ready.State, ready.Red, ready.Lock)
	}
	var whoami ServiceStatus
	for _, s := range status.Services {
		if s.Name == "whoami" {
			whoami = s
		}
	}
	if whoami.State != "rot" || !strings.Contains(strings.Join(whoami.Red, " "), "502") {
		t.Errorf("whoami answers 502 and is not red with the reason: %+v", whoami)
	}
	// the backup: 50 h is no guarantee, and it says where it lies
	lb := ready.LastBackup
	if lb.OK || lb.At == "" || lb.Repos != 1 || lb.Problem == "" || !strings.Contains(lb.Text, "an einem Ort") {
		t.Errorf("a 50 h old backup on one place: %+v", lb)
	}
	if !strings.Contains(strings.Join(ready.Problems, " "), lb.Problem) {
		t.Errorf("the stale backup is red but not among the problems: %q, %q", ready.Problems, lb.Problem)
	}
	// the last change is the last approved proposal, on every surface
	if ready.LastChange.Proposal != 7 || ready.LastChange.Summary == "" {
		t.Errorf("the last change is the approved #7: %+v", ready.LastChange)
	}

	page := html.UnescapeString(get("/"))
	for _, want := range []string{"<h1>Laib · bereit", `class="card rot" id="svc-whoami"`, lb.Text, ready.LastChange.Summary, lb.Problem} {
		if !strings.Contains(page, want) {
			t.Errorf("the Status page lacks %q", want)
		}
	}
	if strings.Contains(page, "von Hand neu gestartet") {
		t.Error("the Status page's last change is the newest journal line again, not the last applied change")
	}

	var text bytes.Buffer
	Status(context.Background(), &cli.Env{Paths: env.Paths, Out: &text}, nil)
	for _, want := range []string{"Laib · bereit", "1 rot", "rot   whoami", "502", lb.Text, ready.LastChange.Summary, lb.Problem} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("laib status lacks %q:\n%s", want, text.String())
		}
	}

	// what diag found red (the mail's source) is red here too: a service's
	// red finding on its row, a box-wide one among the problems
	rows := map[string]ServiceStatus{}
	for _, s := range status.Services {
		rows[s.Name] = s
	}
	for _, f := range mailed {
		if f.Severity != "rot" {
			continue
		}
		if f.Service != "" && rows[f.Service].State != "rot" {
			t.Errorf("diag says %s is red (%s), the pages do not", f.Service, f.Detail)
		}
		if f.Service == "" && !strings.Contains(strings.Join(ready.Problems, "\n"), f.Detail) {
			t.Errorf("diag says the box is red (%s: %s), the pages do not: %q", f.Probe, f.Detail, ready.Problems)
		}
	}
}

// #856: the rules of the one verdict, from facts alone. Readiness means
// what docs/DESIGN.md says (eingeschränkt: apply is locked); red is what
// diag finds red; the backup holds only while no backup finding is red.
func TestTheVerdictFromFacts(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.Local)
	rows := func() []ServiceStatus {
		return []ServiceStatus{
			{Name: "paperless", State: "grün", Units: map[string]string{"paperless.service": "active"}},
			{Name: "media", State: "gelb", Units: map[string]string{"media.service": "activating"}},
			{Name: "llama", State: "pausiert"},
		}
	}
	backup := LastBackup{At: now.Add(-5 * time.Hour).UTC().Format(time.RFC3339), Repos: 1, OnlyOnDataDisk: true}

	r, got := judge(Readiness{}, facts{now: now, rows: rows(), backup: backup, found: []diag.Finding{
		{Severity: "rot", Probe: "health", Service: "paperless", Detail: "502 Bad Gateway"},
		{Severity: "rot", Probe: "status", Service: "media", Detail: "Bibliothek nicht lesbar"},
		{Severity: "rot", Probe: "health", Service: "llama", Detail: "connection refused"}, // paused: it stays paused
		{Severity: "rot", Probe: "unit", Service: "paperless", Detail: "paperless.service ist failed"},
		{Severity: "gelb", Probe: "disk", Detail: "/var hat 12% frei"},
		{Severity: "rot", Probe: "disk", Detail: "/mnt/data hat nur 3.0% frei"},
		{Severity: "rot", Probe: "nftables", Detail: "die Firewall fehlt im Kernel"},
		{Severity: "rot", Probe: "backup", Topic: "offsite", Detail: "keine frische Kopie außer Haus"},
	}})
	if r.State != "bereit" || r.Lock != "" {
		t.Errorf("a missing firewall and red services leave readiness bereit (DESIGN.md), got %s %q", r.State, r.Lock)
	}
	if r.Services != 3 || r.Red != 2 || r.Green != 1 || r.Yellow != 0 {
		t.Errorf("counts: %+v", r)
	}
	if got[0].State != "rot" || got[1].State != "rot" || got[2].State != "pausiert" {
		t.Errorf("states: %+v", got)
	}
	if len(got[0].Red) != 1 || got[0].Red[0] != "health: 502 Bad Gateway" {
		t.Errorf("a unit finding is the units' own line, the health one is the reason: %q", got[0].Red)
	}
	want := []string{"/mnt/data hat nur 3.0% frei", "die Firewall fehlt im Kernel", "keine frische Kopie außer Haus"}
	if strings.Join(r.Problems, "|") != strings.Join(want, "|") {
		t.Errorf("problems: %q, want %q", r.Problems, want)
	}
	// an offsite copy gone stale is a problem, not a failed backup
	if !r.LastBackup.OK || r.LastBackup.Problem != "" {
		t.Errorf("a fresh nightly backup with a stale offsite copy is still a backup: %+v", r.LastBackup)
	}
	// neutral fact, not the family's wording (owner decision haus#173)
	if want := "Letzte Sicherung 28.10. 07:00, an einem Ort: auf der Datenplatte, die sie sichert."; r.LastBackup.Text != want {
		t.Errorf("backup text:\n got %q\nwant %q", r.LastBackup.Text, want)
	}

	r, _ = judge(Readiness{}, facts{now: now, rows: rows(), lock: "apply (claude, pid 4)", backup: LastBackup{At: backup.At, Repos: 2}, found: []diag.Finding{
		{Severity: "rot", Probe: "backup", Detail: "die letzte Sicherung ist fehlgeschlagen oder älter als 36 Stunden"},
	}})
	if r.State != "eingeschränkt" || r.Lock != "apply (claude, pid 4)" {
		t.Errorf("an apply running locks it: %s %q", r.State, r.Lock)
	}
	if r.LastBackup.OK || r.LastBackup.Problem != "die letzte Sicherung ist fehlgeschlagen oder älter als 36 Stunden" ||
		r.LastBackup.Text != "Letzte Sicherung 28.10. 07:00, an zwei Orten. Die letzte Sicherung ist fehlgeschlagen oder älter als 36 Stunden." {
		t.Errorf("a red nightly run is no guarantee: %+v", r.LastBackup)
	}
	if r, _ = judge(Readiness{}, facts{now: now}); r.LastBackup.OK || r.LastBackup.Text != "Noch keine Sicherung." {
		t.Errorf("no backup at all: %+v", r.LastBackup)
	}
}
