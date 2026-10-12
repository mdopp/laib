package lease

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/state"
)

// templateService reads a real template (laib.yml provides, the unit
// files) into a state.Service, so unit names come from the same rule the
// kernel uses (UnitNames) and a renamed template fails this test.
func templateService(t *testing.T, name string) *state.Service {
	t.Helper()
	dir := filepath.Join("..", "..", "templates", name)
	svc := &state.Service{Name: name, Units: map[string]string{}}
	contract, err := os.ReadFile(filepath.Join(dir, "laib.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if m := regexp.MustCompile(`(?m)^provides:\s*\[([^\]]*)\]`).FindStringSubmatch(string(contract)); m != nil {
		for _, p := range strings.Split(m[1], ",") {
			svc.Contract.Provides = append(svc.Contract.Provides, strings.TrimSpace(p))
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		switch filepath.Ext(e.Name()) {
		case ".container", ".pod", ".kube", ".service":
			b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
			svc.Units[e.Name()] = string(b)
		}
	}
	return svc
}

// realUnits resolves a role to the real template that provides it.
func realUnits(t *testing.T) func(role string) (string, []string) {
	t.Helper()
	services := []*state.Service{templateService(t, "stimme"), templateService(t, "llama")}
	return func(role string) (string, []string) {
		for _, svc := range services {
			for _, p := range svc.Contract.Provides {
				if p == role {
					return svc.Name, svc.UnitNames()
				}
			}
		}
		return "", nil
	}
}

func config42(t *testing.T, router string) Config {
	t.Helper()
	home := t.TempDir()
	return Config{
		Household: "gemma-4-e4b",
		Presets:   []Preset{{Alias: "gemma-4-e4b"}, {Alias: "gemma-4-12b"}, {Alias: "qwen3.6-35b-a3b"}},
		Modes: map[string]Mode{
			"foundry":   {Presets: []string{"gemma-4-e4b", "gemma-4-12b"}},
			"erweitert": {Stops: []string{"voice/whisper", "voice/wakeword-trainer", "model/embed"}, Voice: "cpu"},
		},
		Aliases:    map[string]string{"thinking": "erweitert", "coding": "erweitert"},
		VoiceUnits: []string{"voice/whisper", "voice/piper"},
		Units:      realUnits(t),
		RouterURL:  router,
		LeaseDir:   filepath.Join(home, "lease"),
		AutoIdle:   time.Minute,
		Token:      "lease-secret",
		Renew:      10 * time.Minute,
		WarnAfter:  20 * time.Minute,
		Paths:      boxfs.Paths{Home: home, Journal: filepath.Join(home, "journal")},
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

func server42(t *testing.T) (*Server, *httptest.Server, *[]string, Config) {
	t.Helper()
	var calls []string
	Systemctl = func(_ context.Context, verb string, units []string) error {
		calls = append(calls, verb+" "+strings.Join(units, ","))
		return nil
	}
	cfg := config42(t, router42(t).URL)
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return s, srv, &calls, cfg
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
// is refused when the lease starts, not discovered on the GPU box.
func TestModesResolveThroughTheRealTemplates(t *testing.T) {
	s, _, calls, _ := server42(t)
	if _, err := s.Acquire(context.Background(), "bote", "thinking", time.Hour); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(*calls, "\n")
	if !strings.Contains(joined, "stop llama-embed.service,stimme-wakeword-trainer.service,stimme-whisper.service") {
		t.Fatalf("erweitert must stop the real units: %v", *calls)
	}
	if !strings.Contains(joined, "restart stimme-piper.service,stimme-whisper.service") {
		t.Fatalf("the voice units are the real ones: %v", *calls)
	}
	cfg := config42(t, "http://127.0.0.1:1")
	cfg.Modes["erweitert"] = Mode{Stops: []string{"voice/whisper-batch"}}
	if _, err := New(cfg); err == nil || !strings.Contains(err.Error(), "whisper-batch") || !strings.Contains(err.Error(), "stimme") {
		t.Fatalf("a unit that does not exist must be refused at start, naming the service: %v", err)
	}
	cfg.Modes["erweitert"] = Mode{Stops: []string{"stt/whisper"}}
	if _, err := New(cfg); err == nil || !strings.Contains(err.Error(), "stt") {
		t.Fatalf("a role nobody provides must be refused at start: %v", err)
	}
}

// #42 (2): the lease API needs the token; GET and /health stay open.
func TestLeaseAPIRequiresTheToken(t *testing.T) {
	s, srv, _, cfg := server42(t)
	if code, _ := post42(t, srv.URL+"/api/model-lease", "", `{"model":"foundry","holder":"x"}`); code != 401 {
		t.Fatalf("unauthenticated acquire: %d", code)
	}
	if code, _ := post42(t, srv.URL+"/api/model-lease", "wrong", `{"model":"foundry","holder":"x"}`); code != 401 {
		t.Fatalf("wrong token: %d", code)
	}
	if s.read() != nil {
		t.Fatal("a refused acquire must not write a lease")
	}
	code, out := post42(t, srv.URL+"/api/model-lease", cfg.Token, `{"model":"foundry","holder":"x","ttl_s":60}`)
	if code != 200 || out["status"] != "ready" {
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
	s, _, calls, _ := server42(t)
	l, err := s.Acquire(context.Background(), "chronicle", "gemma-4-12b", time.Hour)
	if err != nil || l.Mode != "foundry" {
		t.Fatalf("gemma-4-12b is allowed in foundry (2 presets) and erweitert (all): %+v %v", l, err)
	}
	if strings.Contains(strings.Join(*calls, "\n"), "stop ") {
		t.Fatalf("foundry stops nothing: %v", *calls)
	}
	s.Release(context.Background(), "chronicle")
	l, err = s.Acquire(context.Background(), "bote", "qwen3.6-35b-a3b", time.Hour)
	if err != nil || l.Mode != "erweitert" {
		t.Fatalf("only erweitert allows qwen: %+v %v", l, err)
	}
}

// #42 (3): a household request through the proxy must not keep a
// foreign window alive; only a request that needs the window renews it.
func TestHouseholdRequestsDoNotRenewTheForeignWindow(t *testing.T) {
	s, srv, _, cfg := server42(t)
	if code, _ := post42(t, srv.URL+"/v1/chat/completions", "", `{"model":"qwen3.6-35b-a3b","messages":[]}`); code != 200 {
		t.Fatal("mode did not follow", code)
	}
	before := s.read()
	if before == nil || before.Holder != AutoHolder {
		t.Fatalf("%+v", before)
	}
	s.lastRen = time.Time{} // the renewal gate is open
	if code, _ := post42(t, srv.URL+"/v1/chat/completions", "", `{"model":"`+cfg.Household+`","messages":[]}`); code != 200 {
		t.Fatal(code)
	}
	if after := s.read(); !after.Until.Equal(before.Until) {
		t.Fatalf("a household request renewed the foreign window: %v → %v", before.Until, after.Until)
	}
	s.lastRen = time.Time{}
	post42(t, srv.URL+"/v1/chat/completions", "", `{"model":"qwen3.6-35b-a3b","messages":[]}`)
	if after := s.read(); !after.Until.After(before.Until) {
		t.Fatal("a request that needs the window renews it")
	}
}

// #42 goal: a holder renews or loses the card within one interval, not
// after the 4 h TTL; the response says how often.
func TestAHolderThatStopsRenewingIsReleasedWithinOneInterval(t *testing.T) {
	s, srv, calls, cfg := server42(t)
	s.cfg.Renew = time.Second
	code, out := post42(t, srv.URL+"/api/model-lease", cfg.Token, `{"model":"thinking","holder":"bote","ttl_s":14400}`)
	if code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	if rs, _ := out["renew_s"].(float64); rs != 1 {
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
	post42(t, srv.URL+"/api/model-lease", cfg.Token, `{"model":"thinking","holder":"bote","ttl_s":14400}`)
	if l2 := s.read(); !l2.Until.After(l.Until) || !l2.Deadline.Equal(l.Deadline) {
		t.Fatalf("renewal: %v→%v, deadline %v→%v", l.Until, l2.Until, l.Deadline, l2.Deadline)
	}
	time.Sleep(1100 * time.Millisecond)
	s.expireOnce(context.Background())
	if s.read() != nil {
		t.Fatal("a holder that stopped renewing still holds the card")
	}
	if !strings.Contains(strings.Join(*calls, "\n"), "start llama-embed.service,stimme-wakeword-trainer.service,stimme-whisper.service") {
		t.Fatalf("the units come back on expiry: %v", *calls)
	}
}

// #42 goal: starvation is visible — the pause marker covers every service
// whose units the lease stopped and says why in plain German; a foreign
// window older than WarnAfter gets one yellow journal line.
func TestALongForeignLeaseIsVisible(t *testing.T) {
	s, _, _, cfg := server42(t)
	if _, err := s.Acquire(context.Background(), "bote", "thinking", time.Hour); err != nil {
		t.Fatal(err)
	}
	paused := apply.Paused(cfg.Paths)
	for _, svc := range []string{"stimme", "llama"} {
		p, ok := paused[svc]
		if !ok {
			t.Fatalf("%s must carry the pause marker (its units are stopped): %v", svc, paused)
		}
		if !strings.HasPrefix(p.By, "lease:") || !strings.Contains(p.Reason, "Grafikkarte") || !strings.Contains(p.Reason, "bote") {
			t.Fatalf("the pause says who holds the card and why, in German: %+v", p)
		}
	}
	s.expireOnce(context.Background())
	if es, _ := journal.Read(cfg.Paths.Journal, time.Now().Add(-time.Hour)); len(es) != 0 {
		t.Fatalf("no warning before WarnAfter: %+v", es)
	}
	l := s.read()
	l.Since = time.Now().Add(-cfg.WarnAfter - time.Minute)
	s.write(l)
	s.expireOnce(context.Background())
	s.expireOnce(context.Background())
	es, _ := journal.Read(cfg.Paths.Journal, time.Now().Add(-time.Hour))
	if len(es) != 1 || es[0].Result != "gelb" || es[0].Verb != "lease" || !strings.Contains(es[0].Detail, "bote") || !strings.Contains(es[0].Detail, "Sprache") {
		t.Fatalf("one yellow journal line naming the holder and the consequence: %+v", es)
	}
	s.Release(context.Background(), "bote")
	if len(apply.Paused(cfg.Paths)) != 0 {
		t.Fatal("release lifts every pause the lease wrote")
	}
}
