package lease

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// templateService reads a real template (laib.yml provides, the unit
// files) as laib status lists it, so a renamed template fails this test.
// The unit names follow Quadlet's rule, as the kernel's state.UnitNames
// does (x.container is x.service, x.pod is x-pod.service, a .service
// without [Install] is a oneshot and no unit); the lease imports nothing
// of the kernel (#677), so the rule is written out here.
func templateService(t *testing.T, name string) Service {
	t.Helper()
	dir := filepath.Join("..", "templates", name)
	svc := Service{Name: name, Units: map[string]string{}}
	contract, err := os.ReadFile(filepath.Join(dir, "laib.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if m := regexp.MustCompile(`(?m)^provides:\s*\[([^\]]*)\]`).FindStringSubmatch(string(contract)); m != nil {
		for _, p := range strings.Split(m[1], ",") {
			svc.Provides = append(svc.Provides, strings.TrimSpace(p))
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		file := e.Name()
		base := strings.TrimSuffix(file, filepath.Ext(file))
		switch filepath.Ext(file) {
		case ".container", ".kube":
			svc.Units[base+".service"] = "active"
		case ".pod":
			svc.Units[base+"-pod.service"] = "active"
		case ".service":
			if b, _ := os.ReadFile(filepath.Join(dir, file)); strings.Contains(string(b), "[Install]") {
				svc.Units[file] = "active"
			}
		}
	}
	return svc
}

// realServices are the real templates as laib status lists them: their
// names, roles and units.
func realServices(t *testing.T) []Service {
	t.Helper()
	return []Service{templateService(t, "stimme"), templateService(t, "llama")}
}

func config42(t *testing.T, router string) Config {
	t.Helper()
	home := t.TempDir()
	return Config{
		Household: "gemma-4-e4b",
		Presets:   []Preset{{Alias: "gemma-4-e4b"}, {Alias: "gemma-4-12b"}, {Alias: "qwen3.6-35b-a3b"}},
		Modes: map[string]Mode{
			"shared": {Presets: []string{"gemma-4-e4b", "gemma-4-12b"}},
			"full":   {Stops: []string{"voice/whisper", "voice/wakeword-trainer", "model/embed"}, Voice: "cpu"},
		},
		VoiceUnits: []string{"voice/whisper", "voice/piper"},
		Laib:       newFake(realServices(t)),
		RouterURL:  router,
		LeaseDir:   filepath.Join(home, "lease"),
		Token:      "lease-secret",
		Renew:      10 * time.Minute,
		WarnAfter:  20 * time.Minute,
	}
}

func router42(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/models" {
			w.Write([]byte(`{"object":"list","data":[{"id":"gemma-4-e4b"},{"id":"qwen3.6-35b-a3b"},{"id":"gemma-4-12b"}]}`))
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func server42(t *testing.T) (*Server, *httptest.Server, *fakeLaib, Config) {
	t.Helper()
	cfg := config42(t, router42(t).URL)
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return s, srv, cfg.Laib.(*fakeLaib), cfg
}

func post42(t *testing.T, url, token, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// #42 (1): modes name roles and containers; the lease resolves them to
// unit names through the real templates, and a name that does not exist
// is refused when the lease starts, not discovered on the GPU box. Since
// ADR 0027 the lease stops nothing itself: it pauses each service through
// laib, naming the units to stop and, for the voice on the CPU, the ones
// to restart once voice-device.env says cpu.
func TestModesResolveThroughTheRealTemplates(t *testing.T) {
	s, _, laib, cfg := server42(t)
	laib.onPause = func(h Hold) {
		if b, _ := os.ReadFile(filepath.Join(cfg.LeaseDir, "voice-device.env")); h.Service == "stimme" && !strings.Contains(string(b), "WHISPER_DEVICE=cpu") {
			t.Errorf("stimme is restarted before voice-device.env says cpu: %q", b)
		}
	}
	if _, err := s.Acquire(context.Background(), "bote", "full", time.Hour); err != nil {
		t.Fatal(err)
	}
	if want := []string{"pause llama stop llama-embed.service", "pause stimme stop stimme-wakeword-trainer.service,stimme-whisper.service restart stimme-piper.service,stimme-whisper.service"}; !slices.Equal(laib.calls, want) {
		t.Fatalf("full must pause the real services with their real units:\n%s", strings.Join(laib.calls, "\n"))
	}
	cfg = config42(t, "http://127.0.0.1:1")
	cfg.Modes["full"] = Mode{Stops: []string{"voice/whisper-batch"}}
	if _, err := New(cfg); err == nil || !strings.Contains(err.Error(), "whisper-batch") || !strings.Contains(err.Error(), "stimme") {
		t.Fatalf("a unit that does not exist must be refused at start, naming the service: %v", err)
	}
	// a role nobody provides degrades the mode instead (#374, lease374_test.go)
}

// #42 (2): the lease API needs the token; GET and /health stay open.
func TestLeaseAPIRequiresTheToken(t *testing.T) {
	s, srv, _, cfg := server42(t)
	if code, _ := post42(t, srv.URL+"/api/model-lease", "", `{"model":"shared","holder":"x"}`); code != 401 {
		t.Fatalf("unauthenticated acquire: %d", code)
	}
	if code, _ := post42(t, srv.URL+"/api/model-lease", "wrong", `{"model":"shared","holder":"x"}`); code != 401 {
		t.Fatalf("wrong token: %d", code)
	}
	if s.read() != nil {
		t.Fatal("a refused acquire must not write a lease")
	}
	code, out := post42(t, srv.URL+"/api/model-lease", cfg.Token, `{"model":"shared","holder":"x","ttl_s":60}`)
	if code != 200 || out["state"] != "ready" {
		t.Fatalf("%d %v", code, out)
	}
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/model-lease", strings.NewReader(`{"holder":"x"}`))
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 401 || s.read() == nil {
		t.Fatalf("unauthenticated release: %d", resp.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/api/model-lease", strings.NewReader(`{"holder":"x"}`))
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 200 || s.read() != nil {
		t.Fatalf("release with the token: %d", resp.StatusCode)
	}
	for _, p := range []string{"/api/model-lease", "/health"} {
		if resp, _ := http.Get(srv.URL + p); resp.StatusCode != 200 {
			t.Fatalf("GET %s must stay open: %d", p, resp.StatusCode)
		}
	}
	cfg.Token = ""
	if _, err := New(cfg); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("no token, no lease server: %v", err)
	}
}

// #42 (3): acquiring by preset takes the narrowest mode that allows it,
// not the first by name.
func TestAcquireByPresetTakesTheNarrowestMode(t *testing.T) {
	s, _, laib, _ := server42(t)
	l, err := s.Acquire(context.Background(), "chronicle", "gemma-4-12b", time.Hour)
	if err != nil || l.Mode != "shared" {
		t.Fatalf("gemma-4-12b is allowed in shared (2 presets) and full (all): %+v %v", l, err)
	}
	if len(laib.calls) != 0 {
		t.Fatalf("shared pauses nothing: %v", laib.calls)
	}
	s.Release(context.Background(), "chronicle")
	l, err = s.Acquire(context.Background(), "bote", "qwen3.6-35b-a3b", time.Hour)
	if err != nil || l.Mode != "full" {
		t.Fatalf("only full allows qwen: %+v %v", l, err)
	}
}

// #522: a mode other than the household's is never silent. A standing
// lease is yellow in the status file laib status and diag read, with its
// holder, window and end; giving it back clears it.
func TestAStandingLeaseIsYellowInTheStatusFile(t *testing.T) {
	s, _, _, cfg := server42(t)
	status := func() map[string]any {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(cfg.LeaseDir, "laib-status.json"))
		if err != nil {
			t.Fatal(err)
		}
		var f map[string]any
		json.Unmarshal(b, &f)
		return f
	}
	if f := status(); len(f["findings"].([]any)) != 0 {
		t.Fatalf("the household has nothing to report: %v", f)
	}
	if _, err := s.Acquire(context.Background(), "bote", "qwen3.6-35b-a3b", time.Hour); err != nil {
		t.Fatal(err)
	}
	f := status()
	got, _ := json.Marshal(f["findings"])
	for _, want := range []string{`"severity":"gelb"`, "Modus full", "bote", "qwen3.6-35b-a3b", "stimme", "DELETE /api/model-lease"} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("the lease's finding lacks %q: %s", want, got)
		}
	}
	s.Release(context.Background(), "bote")
	if f := status(); len(f["findings"].([]any)) != 0 {
		t.Fatalf("a released lease still reported: %v", f)
	}
}

// #522: the automatic window a request took before is given back at
// start; voice returns to the GPU and its units start again.
func TestAStaleRouterWindowIsReleasedAtStart(t *testing.T) {
	cfg := config42(t, router42(t).URL)
	laib := cfg.Laib.(*fakeLaib)
	os.MkdirAll(cfg.LeaseDir, 0o755)
	stale, _ := json.Marshal(Lease{Holder: "router", Mode: "full", Since: time.Now(), Until: time.Now().Add(45 * time.Minute)})
	os.WriteFile(filepath.Join(cfg.LeaseDir, "lease.json"), stale, 0o644)
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if l := s.read(); l != nil {
		t.Fatalf("the router's window stands: %+v", l)
	}
	if env, _ := os.ReadFile(filepath.Join(cfg.LeaseDir, "voice-device.env")); !strings.Contains(string(env), "cuda") || !slices.Equal(laib.calls, []string{"resume llama", "resume stimme"}) {
		t.Fatalf("voice not back on the GPU or the services not resumed: %q %v", env, laib.calls)
	}
}

// #42 goal: a holder renews or loses the card within one interval, not
// after the 4 h TTL; the response says how often.
func TestAHolderThatStopsRenewingIsReleasedWithinOneInterval(t *testing.T) {
	s, srv, laib, cfg := server42(t)
	s.cfg.Renew = time.Second
	code, out := post42(t, srv.URL+"/api/model-lease", cfg.Token, `{"model":"full","holder":"bote","ttl_s":14400}`)
	if code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	if rs, _ := out["renew_after"].(float64); rs != 1 {
		t.Fatalf("the answer must say when to renew: %v", out)
	}
	l := s.read()
	if l.Until.After(time.Now().Add(2 * time.Second)) {
		t.Fatalf("until must be one renewal interval away, not the ttl: %v", l.Until)
	}
	if l.Deadline.Before(time.Now().Add(3 * time.Hour)) {
		t.Fatalf("the ttl stays as the hard deadline: %v", l.Deadline)
	}
	// renewing moves until, not the deadline
	time.Sleep(50 * time.Millisecond)
	post42(t, srv.URL+"/api/model-lease", cfg.Token, `{"model":"full","holder":"bote","ttl_s":14400}`)
	if l2 := s.read(); !l2.Until.After(l.Until) || !l2.Deadline.Equal(l.Deadline) {
		t.Fatalf("renewal: %v→%v, deadline %v→%v", l.Until, l2.Until, l.Deadline, l2.Deadline)
	}
	time.Sleep(1100 * time.Millisecond)
	s.expireOnce(context.Background())
	if s.read() != nil {
		t.Fatal("a holder that stopped renewing still holds the card")
	}
	if !slices.Contains(laib.calls, "pause stimme") || !slices.Contains(laib.calls, "resume llama") || !slices.Contains(laib.calls, "resume stimme") {
		t.Fatalf("a renewal renews the pauses, expiry resumes the services: %v", laib.calls)
	}
}

// #42 goal: starvation is visible — the pause marker covers every service
// whose units the lease stopped and says why in plain German; a foreign
// window older than WarnAfter gets one yellow journal line.
func TestALongForeignLeaseIsVisible(t *testing.T) {
	s, _, laib, cfg := server42(t)
	if _, err := s.Acquire(context.Background(), "bote", "full", time.Hour); err != nil {
		t.Fatal(err)
	}
	for _, svc := range []string{"stimme", "llama"} {
		p, ok := laib.paused[svc]
		if !ok {
			t.Fatalf("%s must be paused through laib (its units are stopped): %v", svc, laib.paused)
		}
		if !strings.Contains(p.Reason, "Grafikkarte") || !strings.Contains(p.Reason, "bote") || !p.Until.Equal(s.read().Until) {
			t.Fatalf("the pause says who holds the card and why, in German, until the renewal is due: %+v", p)
		}
	}
	s.expireOnce(context.Background())
	if len(laib.notes) != 0 {
		t.Fatalf("no warning before WarnAfter: %v", laib.notes)
	}
	l := s.read()
	l.Since = time.Now().Add(-cfg.WarnAfter - time.Minute)
	s.write(l)
	s.expireOnce(context.Background())
	s.expireOnce(context.Background())
	if len(laib.notes) != 1 || !strings.Contains(laib.notes[0], "bote") || !strings.Contains(laib.notes[0], "Sprache") {
		t.Fatalf("one note in laib's journal naming the holder and the consequence: %v", laib.notes)
	}
	s.Release(context.Background(), "bote")
	if len(laib.paused) != 0 {
		t.Fatal("release resumes every service the lease paused")
	}
}
