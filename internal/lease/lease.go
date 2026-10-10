// Package lease is the llama template's sidecar (ADR 0011): the policy
// proxy in front of llama-server's router and the GPU lease, in one
// long-running process. Ported from solarisbay's post-deploy `proxy`,
// `acquire` and `release` verbs; the file-based broker and the systemd
// expiry unit are gone because this process is always there.
//
// Contract (mdopp/foundry-chronicle#321): POST /api/model-lease
// {"model": "<mode or preset>", "ttl_s": 900, "holder": "name"} answers
// 200 ready, 202 preparing (retry_after) or 409 held; GET shows the lease;
// DELETE {"holder": …} releases that holder's lease, a bodyless DELETE
// releases unconditionally. Every /v1 request names a model; outside the
// standing mode it is refused with 409, except that from the household
// (no lease) the proxy takes the `erweitert` window itself for a preset
// the router serves (mode follows the request, idle timeout).
package lease

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Preset is one model the router serves.
type Preset struct {
	Alias string `json:"alias"`
	Label string `json:"label"`
}

// Mode is what a lease sets: which presets are allowed and what else changes.
type Mode struct {
	Presets   []string `json:"presets"`   // empty = every preset
	StopUnits []string `json:"stopUnits"` // systemd --user units stopped while the mode stands
	Voice     string   `json:"voice"`     // gpu | cpu → VOICE_DEVICE_FILE
}

// Config is presets.json plus the environment.
type Config struct {
	Household string            `json:"household"` // the household preset alias
	Presets   []Preset          `json:"presets"`
	Modes     map[string]Mode   `json:"modes"`
	Aliases   map[string]string `json:"aliases"` // thinking → erweitert, coding → erweitert
	RouterURL string            `json:"-"`
	LeaseDir  string            `json:"-"`
	AutoIdle  time.Duration     `json:"-"` // idle timeout of the automatic window
}

// Lease is the standing lease, persisted as lease.json.
type Lease struct {
	Holder  string    `json:"holder"`
	Mode    string    `json:"mode"`
	Alias   string    `json:"alias"`
	Allowed []string  `json:"allowed"`
	Since   time.Time `json:"since"`
	Until   time.Time `json:"until"`
	Ready   bool      `json:"ready"`
}

// AutoHolder names the window the proxy takes itself.
const AutoHolder = "router"

// Systemctl is the seam to `systemctl --user`; tests swap it.
var Systemctl = func(ctx context.Context, verb string, units []string) error {
	if len(units) == 0 {
		return nil
	}
	out, err := exec.CommandContext(ctx, "systemctl", append([]string{"--user", verb}, units...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %s", verb, strings.TrimSpace(string(out)))
	}
	return nil
}

// Server holds the state.
type Server struct {
	cfg     Config
	mu      sync.Mutex
	lastRen time.Time
	proxy   *httputil.ReverseProxy
	client  *http.Client
}

// New builds the server; the router URL must be reachable.
func New(cfg Config) (*Server, error) {
	if cfg.Household == "" || len(cfg.Presets) == 0 {
		return nil, errors.New("presets.json needs household and presets")
	}
	if cfg.AutoIdle <= 0 {
		cfg.AutoIdle = 30 * time.Minute
	}
	u, err := url.Parse(cfg.RouterURL)
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, proxy: httputil.NewSingleHostReverseProxy(u), client: &http.Client{Timeout: 10 * time.Minute}}
	s.proxy.FlushInterval = -1 // token streams
	go s.expiryLoop()
	return s, nil
}

func (s *Server) leasePath() string { return filepath.Join(s.cfg.LeaseDir, "lease.json") }

// read returns the standing lease or nil.
func (s *Server) read() *Lease {
	b, err := os.ReadFile(s.leasePath())
	if err != nil {
		return nil
	}
	var l Lease
	if json.Unmarshal(b, &l) != nil || l.Holder == "" {
		return nil
	}
	return &l
}

func (s *Server) write(l *Lease) error {
	os.MkdirAll(s.cfg.LeaseDir, 0o755)
	b, _ := json.MarshalIndent(l, "", "  ")
	tmp := s.leasePath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.leasePath())
}

func (s *Server) clear() { os.Remove(s.leasePath()) }

// canonical maps thinking/coding onto erweitert; unknown names stay.
func (s *Server) canonical(name string) string {
	name = strings.TrimSpace(name)
	if m, ok := s.cfg.Aliases[name]; ok {
		return m
	}
	return name
}

func (s *Server) knownPreset(alias string) bool {
	for _, p := range s.cfg.Presets {
		if p.Alias == alias {
			return true
		}
	}
	return false
}

func (s *Server) allPresets() []string {
	var out []string
	for _, p := range s.cfg.Presets {
		out = append(out, p.Alias)
	}
	return out
}

// allowed lists the presets a mode permits.
func (s *Server) allowed(mode string) []string {
	m, ok := s.cfg.Modes[mode]
	if !ok {
		return []string{s.cfg.Household}
	}
	if len(m.Presets) == 0 {
		return s.allPresets()
	}
	return append([]string(nil), m.Presets...)
}

// Policy answers (allowed presets, standing mode) for the next request.
func (s *Server) Policy() ([]string, string) {
	l := s.read()
	if l == nil {
		return []string{s.cfg.Household}, "haushalt"
	}
	return l.Allowed, l.Mode
}

// Acquire hands the card to holder for mode (or a preset name that implies
// its mode) for ttl. Errors: ErrHeld when another holder stands.
var ErrHeld = errors.New("held")

func (s *Server) Acquire(ctx context.Context, holder, want string, ttl time.Duration) (*Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.read()
	if cur != nil && cur.Holder != holder && cur.Holder != AutoHolder {
		return cur, ErrHeld
	}
	// a preset name implies the first mode that allows it
	mode := s.canonical(want)
	alias := ""
	if _, isMode := s.cfg.Modes[mode]; !isMode {
		if s.knownPreset(want) {
			alias = want
			mode = ""
			for _, name := range s.sortedModes() {
				for _, p := range s.allowed(name) {
					if p == want {
						mode = name
					}
				}
				if mode != "" {
					break
				}
			}
			if mode == "" {
				return nil, fmt.Errorf("no mode allows preset %q", want)
			}
		} else if want != "" {
			return nil, fmt.Errorf("unknown mode or preset %q; known modes: %s", want, strings.Join(s.sortedModes(), ", "))
		}
	}
	now := time.Now()
	if cur != nil && s.canonical(cur.Mode) == mode && cur.Ready {
		cur.Holder, cur.Until = holder, now.Add(ttl)
		if alias != "" {
			cur.Alias = alias
		}
		return cur, s.write(cur)
	}
	left := Mode{}
	if cur != nil {
		left = s.cfg.Modes[s.canonical(cur.Mode)]
	}
	m := s.cfg.Modes[mode]
	l := &Lease{Holder: holder, Mode: mode, Alias: alias, Allowed: s.allowed(mode), Since: now, Until: now.Add(ttl)}
	if mode == "" { // exclusive: everything stops, nothing answers
		l.Mode, l.Allowed = "exclusive", []string{}
		if err := s.write(l); err != nil {
			return nil, err
		}
		Systemctl(ctx, "stop", s.exclusiveUnits())
		l.Ready = true
		return l, s.write(l)
	}
	if err := s.write(l); err != nil {
		return nil, err
	}
	if len(m.StopUnits) > 0 {
		Systemctl(ctx, "stop", m.StopUnits)
	} else if len(left.StopUnits) > 0 {
		Systemctl(ctx, "start", left.StopUnits)
	}
	if m.Voice == "cpu" {
		s.voice("cpu")
	} else if left.Voice == "cpu" {
		s.voice("gpu")
	}
	l.Ready = true
	return l, s.write(l)
}

func (s *Server) sortedModes() []string {
	var names []string
	for n := range s.cfg.Modes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (s *Server) exclusiveUnits() []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range s.cfg.Modes {
		for _, u := range m.StopUnits {
			if !seen[u] {
				seen[u] = true
				out = append(out, u)
			}
		}
	}
	sort.Strings(out)
	return out
}

// voice writes the device file the voice units read as an EnvironmentFile.
func (s *Server) voice(dev string) {
	env := "WHISPER_DEVICE=cuda\nTTS_DEVICE=cuda\n"
	if dev == "cpu" {
		env = "WHISPER_DEVICE=cpu\nTTS_DEVICE=cpu\n"
	}
	os.WriteFile(filepath.Join(s.cfg.LeaseDir, "voice-device.env"), []byte(env), 0o644)
	Systemctl(context.Background(), "restart", s.voiceUnits())
}

// voiceUnits are the Wyoming units that read voice-device.env (env VOICE_UNITS).
func (s *Server) voiceUnits() []string {
	var out []string
	if v := os.Getenv("VOICE_UNITS"); v != "" {
		for _, u := range strings.Split(v, ",") {
			if u = strings.TrimSpace(u); u != "" {
				out = append(out, u)
			}
		}
	}
	return out
}

// Release gives the card back: start what the mode stopped, voice to gpu,
// drop the lease last.
func (s *Server) Release(ctx context.Context, holder string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.read()
	if cur == nil {
		return nil
	}
	if holder != "" && cur.Holder != holder {
		return fmt.Errorf("lease is held by %q, not %q", cur.Holder, holder)
	}
	if cur.Mode == "exclusive" {
		Systemctl(ctx, "start", s.exclusiveUnits())
	} else if m, ok := s.cfg.Modes[s.canonical(cur.Mode)]; ok {
		if len(m.StopUnits) > 0 {
			Systemctl(ctx, "start", m.StopUnits)
		}
		if m.Voice == "cpu" {
			s.voice("gpu")
		}
	}
	s.clear()
	return nil
}

func (s *Server) expiryLoop() {
	for {
		time.Sleep(30 * time.Second)
		if l := s.read(); l != nil && time.Now().After(l.Until) {
			s.Release(context.Background(), "")
		}
	}
}

// Handler serves the lease API and the policy proxy.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/model-lease", s.leaseAPI)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		_, mode := s.Policy()
		json.NewEncoder(w).Encode(map[string]string{"lease": "bereit", "mode": mode})
	})
	mux.HandleFunc("/", s.proxyRequest)
	return mux
}

func (s *Server) leaseAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		l := s.read()
		if l == nil {
			allowed, mode := s.Policy()
			json.NewEncoder(w).Encode(map[string]any{"holder": "", "mode": mode, "allowed": allowed, "alias": s.cfg.Household})
			return
		}
		json.NewEncoder(w).Encode(l)
	case http.MethodPost:
		var in struct {
			Model  string `json:"model"`
			TTL    int    `json:"ttl_s"`
			Holder string `json:"holder"`
		}
		json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&in)
		if in.Holder == "" {
			in.Holder = "anonymous"
		}
		ttl := time.Duration(in.TTL) * time.Second
		if ttl <= 0 {
			ttl = 4 * time.Hour
		}
		l, err := s.Acquire(r.Context(), in.Holder, in.Model, ttl)
		if errors.Is(err, ErrHeld) {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]any{"status": "held", "holder": l.Holder, "mode": l.Mode, "allowed": l.Allowed, "until": l.Until})
			return
		}
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		alias := l.Alias
		if alias == "" && len(l.Allowed) > 0 {
			alias = l.Allowed[0]
		}
		if alias == "" {
			// exclusive: nothing answers; ready means "the card is yours"
			json.NewEncoder(w).Encode(map[string]any{"status": "ready", "alias": "", "mode": l.Mode, "allowed": l.Allowed, "until": l.Until})
			return
		}
		if !s.warm(r.Context(), alias) {
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]any{"status": "preparing", "retry_after": 10, "alias": alias, "mode": l.Mode, "allowed": l.Allowed})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "ready", "alias": alias, "mode": l.Mode, "allowed": l.Allowed, "until": l.Until})
	case http.MethodDelete:
		var in struct {
			Holder string `json:"holder"`
		}
		json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&in)
		if err := s.Release(r.Context(), in.Holder); err != nil {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "released"})
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
	}
}

// warm asks the router for one token from the preset; false when it does
// not answer within a few seconds (the weights are still loading).
func (s *Server) warm(ctx context.Context, alias string) bool {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]any{"model": alias, "max_tokens": 1, "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.cfg.RouterURL, "/")+"/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (s *Server) proxyRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && (r.URL.Path == "/v1/models" || r.URL.Path == "/models") {
		s.catalogue(w, r)
		return
	}
	if r.Method != http.MethodPost {
		s.proxy.ServeHTTP(w, r)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	r.Body = io.NopCloser(bytes.NewReader(body))
	var req struct {
		Model string `json:"model"`
	}
	json.Unmarshal(body, &req)
	model := strings.TrimSpace(req.Model)
	allowed, mode := s.Policy()
	if model != "" && !contains(allowed, model) {
		if !s.modeFollows(r.Context(), model) {
			holder := ""
			if l := s.read(); l != nil {
				holder = l.Holder
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(s.denial(model, mode, allowed, holder))
			return
		}
	}
	s.renewIfOurs(r.Context())
	s.proxy.ServeHTTP(w, r)
}

// modeFollows: from the household, a request for a preset the router
// serves takes the erweitert window itself; a window a named holder has
// stands.
func (s *Server) modeFollows(ctx context.Context, want string) bool {
	if !s.knownPreset(want) {
		return false
	}
	if l := s.read(); l != nil {
		return contains(l.Allowed, want)
	}
	auto, ok := s.autoMode()
	if !ok {
		return false
	}
	_, err := s.Acquire(ctx, AutoHolder, auto, s.cfg.AutoIdle*3/2)
	s.lastRen = time.Now()
	return err == nil
}

// autoMode is the mode that allows every preset, if one exists.
func (s *Server) autoMode() (string, bool) {
	for _, name := range s.sortedModes() {
		if len(s.cfg.Modes[name].Presets) == 0 {
			return name, true
		}
	}
	return "", false
}

func (s *Server) renewIfOurs(ctx context.Context) {
	if time.Since(s.lastRen) < time.Minute {
		return
	}
	l := s.read()
	if l == nil || l.Holder != AutoHolder {
		return
	}
	s.Acquire(ctx, AutoHolder, l.Mode, s.cfg.AutoIdle*3/2)
	s.lastRen = time.Now()
}

func (s *Server) denial(model, mode string, allowed []string, holder string) map[string]any {
	var say, remedy string
	switch {
	case len(allowed) == 0:
		say, remedy = "Die Grafikkarte ist exklusiv vergeben; es antwortet gerade kein Modell.", "Die Lease zeigt, bis wann."
	case !s.knownPreset(model):
		say, remedy = "Dieses Modell kennt der Router nicht; er kennt: "+strings.Join(s.allPresets(), ", ")+".", "Eines davon im Feld `model` angeben."
	default:
		say = "Erlaubt ist: " + strings.Join(allowed, ", ") + "."
		remedy = "Eines davon im Feld `model` angeben."
		if holder != "" {
			remedy = "„" + holder + "“ hat die Grafikkarte gerade; die Lease zeigt, bis wann."
		}
	}
	return map[string]any{"error": map[string]any{"message": fmt.Sprintf("Modell %s ist im Modus %s nicht erlaubt. %s %s", model, mode, say, remedy), "mode": mode, "allowed": allowed}}
}

// catalogue lists every preset the router knows, marked allowed_in_mode.
func (s *Server) catalogue(w http.ResponseWriter, r *http.Request) {
	allowed, mode := s.Policy()
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, strings.TrimRight(s.cfg.RouterURL, "/")+"/v1/models", nil)
	resp, err := s.client.Do(req)
	var listing map[string]any
	if err == nil {
		defer resp.Body.Close()
		json.NewDecoder(resp.Body).Decode(&listing)
	}
	if listing == nil {
		listing = map[string]any{"object": "list", "data": []any{}}
		var data []any
		for _, p := range s.cfg.Presets {
			data = append(data, map[string]any{"id": p.Alias, "object": "model"})
		}
		listing["data"] = data
	}
	listing["mode"] = mode
	if data, ok := listing["data"].([]any); ok {
		for i, e := range data {
			if m, ok := e.(map[string]any); ok {
				id, _ := m["id"].(string)
				m["allowed_in_mode"] = contains(allowed, id)
				data[i] = m
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(listing)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
