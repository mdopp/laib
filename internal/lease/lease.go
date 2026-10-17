// Package lease is the llama template's sidecar (ADR 0011): the policy
// proxy in front of llama-server's router and the GPU lease, in one
// long-running process. Ported from solarisbay's post-deploy `proxy`,
// `acquire` and `release` verbs; the file-based broker and the systemd
// expiry unit are gone because this process is always there.
//
// The lease API speaks the lease contract (ADR 0011, amendment
// 2026-10-14; #157): POST /api/model-lease {"model", "ttl_s", "holder"}
// with the model key answers 200 ready, 202 preparing or 409 held; GET
// shows the window; DELETE {"holder"} gives it back. `model` is a preset
// (the lease takes the narrowest window that allows it) or a window name.
// Every /v1 request names a model; outside the standing window it is
// refused with 409, except that from the household (no lease) the proxy
// takes the window that allows every preset itself (mode follows the
// request, idle timeout).
package lease

import (
	"bytes"
	"cmp"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/journal"
)

// Preset is one model the router serves.
type Preset struct {
	Alias string `json:"alias"`
	Label string `json:"label"`
}

// Mode is what a lease sets: which presets are allowed and what else changes.
type Mode struct {
	Presets []string `json:"presets"` // empty = every preset
	// Stops names what the mode takes the card from, as role/container
	// (voice/whisper, model/embed): the lease resolves them to unit names
	// through laib's state (#42), so a renamed template cannot silently
	// leave a unit running on the GPU.
	Stops []string `json:"stops"`
	Voice string   `json:"voice"` // gpu | cpu → voice-device.env
	Label string   `json:"label"` // what the family reads in the pause reason
}

// Config is presets.json plus the environment.
type Config struct {
	Household string          `json:"household"` // the household preset alias
	Presets   []Preset        `json:"presets"`
	Modes     map[string]Mode `json:"modes"`
	// VoiceUnits are the role/container refs that read voice-device.env
	// (voice/whisper, voice/piper); restarted when the device changes.
	VoiceUnits []string `json:"voiceUnits"`
	// Units resolves a role to the service that provides it and its unit
	// names; main wires it to laib's state, tests to the real templates.
	Units     func(role string) (service string, units []string) `json:"-"`
	RouterURL string                                             `json:"-"`
	LeaseDir  string                                             `json:"-"`
	AutoIdle  time.Duration                                      `json:"-"` // idle timeout of the automatic window
	Token     string                                             `json:"-"` // bearer for POST/DELETE on the lease API (#42)
	Renew     time.Duration                                      `json:"-"` // a holder renews within this, or loses the card
	WarnAfter time.Duration                                      `json:"-"` // a foreign window older than this is journaled yellow
	Paths     boxfs.Paths                                        `json:"-"` // pause markers and the journal
}

// Lease is the standing lease, persisted as lease.json.
type Lease struct {
	Holder   string    `json:"holder"`
	Mode     string    `json:"mode"`
	Alias    string    `json:"alias"`
	Allowed  []string  `json:"allowed"`
	Since    time.Time `json:"since"`
	Until    time.Time `json:"until"`    // the next renewal is due before this (#42)
	Deadline time.Time `json:"deadline"` // the ttl the holder asked for: never past this
	Ready    bool      `json:"ready"`
	// LastRenewed and RenewAfter are the heartbeat the contract reports:
	// the holder posts again every RenewAfter seconds.
	LastRenewed time.Time `json:"last_renewed"`
	RenewAfter  int       `json:"renew_after"`
	Paused      []string  `json:"paused,omitempty"` // services whose units the lease stopped
	Warned      bool      `json:"warned,omitempty"` // the yellow journal line is written
}

// AutoHolder names the window the proxy takes itself.
const AutoHolder = "router"

// Exclusive is the window that takes the whole card: every window's units
// stop and no preset answers. It is asked for by name, never implied.
const Exclusive = "exclusive"

// The contract's bounds (ADR 0011, amendment 2026-10-14).
const (
	ttlMin, ttlMax, ttlDefault = 300, 86400, 14400 // seconds
	retryAfter                 = 30                // seconds a preparing caller waits before it asks GET
)

// holderRe is a service's name, never a session, round or person.
var holderRe = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

// ErrUnknownModel: neither a window nor a preset the router serves.
var ErrUnknownModel = errors.New("unknown model")

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
	warmed  sync.Map                       // preset → answered once: GET does not ask the router again
	units   map[string][]string            // mode → resolved unit names, sorted
	byMode  map[string]map[string][]string // mode → service → its units
	voice   []string                       // resolved voice units
}

// resolve turns role/container refs into (service → unit names); a ref
// nobody provides or a container the service does not have is an error,
// so a stale presets.json stops the lease at start, not on the GPU.
func (s *Server) resolve(refs []string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, ref := range refs {
		role, container, ok := strings.Cut(ref, "/")
		if !ok || role == "" || container == "" {
			return nil, fmt.Errorf("presets.json: %q is not role/container", ref)
		}
		if s.cfg.Units == nil {
			return nil, errors.New("no unit resolver")
		}
		service, units := s.cfg.Units(role)
		if service == "" {
			return nil, fmt.Errorf("presets.json: no service provides %q (%s)", role, ref)
		}
		want := service + "-" + container + ".service"
		if container == service {
			want = service + ".service"
		}
		if !slices.Contains(units, want) {
			return nil, fmt.Errorf("presets.json: %s has no unit %s (%s); it has %s", service, want, ref, strings.Join(units, ", "))
		}
		out[service] = append(out[service], want)
	}
	return out, nil
}

func flatten(m map[string][]string) []string {
	var out []string
	for _, us := range m {
		out = append(out, us...)
	}
	sort.Strings(out)
	return out
}

// New builds the server; the router URL must be reachable.
func New(cfg Config) (*Server, error) {
	if cfg.Household == "" || len(cfg.Presets) == 0 {
		return nil, errors.New("presets.json needs household and presets")
	}
	if cfg.AutoIdle <= 0 {
		cfg.AutoIdle = 30 * time.Minute
	}
	if cfg.Token == "" {
		return nil, errors.New("the lease token (LEASE_TOKEN) is empty: the lease API would be open to the LAN (#42)")
	}
	if cfg.Renew <= 0 {
		cfg.Renew = 10 * time.Minute
	}
	if cfg.WarnAfter <= 0 {
		cfg.WarnAfter = 20 * time.Minute
	}
	if cfg.Paths.Home == "" {
		cfg.Paths = boxfs.Default()
	}
	u, err := url.Parse(cfg.RouterURL)
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, proxy: httputil.NewSingleHostReverseProxy(u), client: &http.Client{Timeout: 10 * time.Minute}, units: map[string][]string{}, byMode: map[string]map[string][]string{}}
	s.proxy.FlushInterval = -1 // token streams
	for name, m := range cfg.Modes {
		by, err := s.resolve(m.Stops)
		if err != nil {
			return nil, fmt.Errorf("mode %s: %w", name, err)
		}
		s.byMode[name] = by
		s.units[name] = flatten(by)
	}
	voice, err := s.resolve(cfg.VoiceUnits)
	if err != nil {
		return nil, fmt.Errorf("voiceUnits: %w", err)
	}
	s.voice = flatten(voice)
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
	return boxfs.WriteAtomic(s.leasePath()+".tmp", s.leasePath(), b, 0o644)
}

func (s *Server) clear() { os.Remove(s.leasePath()) }

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
	// a preset name implies the narrowest mode that allows it (#42): the
	// Bote's thinking model must not open the widest window by accident
	mode := strings.TrimSpace(want)
	alias := ""
	if _, isMode := s.cfg.Modes[mode]; !isMode && mode != Exclusive {
		if !s.knownPreset(want) {
			return nil, fmt.Errorf("%w %q: a preset (%s) or a window (%s, %s)", ErrUnknownModel, want, strings.Join(s.allPresets(), ", "), strings.Join(s.sortedModes(), ", "), Exclusive)
		}
		alias = want
		mode = ""
		for _, name := range s.sortedModes() {
			if !slices.Contains(s.allowed(name), want) {
				continue
			}
			if mode == "" || len(s.allowed(name)) < len(s.allowed(mode)) {
				mode = name
			}
		}
		if mode == "" {
			return nil, fmt.Errorf("%w: no window allows preset %q", ErrUnknownModel, want)
		}
	}
	now := time.Now()
	if cur != nil && cur.Mode == mode && cur.Ready {
		// a renewal: until moves one interval, the deadline stands (#42)
		if cur.Holder != holder || cur.Deadline.IsZero() || holder == AutoHolder {
			cur.Deadline = now.Add(ttl) // the automatic window is an idle timeout
		}
		cur.Holder, cur.Until = holder, s.until(holder, now, cur.Deadline)
		cur.LastRenewed, cur.RenewAfter = now, s.renewAfter(ttl)
		if alias != "" {
			cur.Alias = alias
		}
		s.pause(cur)
		return cur, s.write(cur)
	}
	left := Mode{}
	leftUnits := map[string][]string{}
	if cur != nil {
		left = s.cfg.Modes[cur.Mode]
		leftUnits = s.byMode[cur.Mode]
	}
	m := s.cfg.Modes[mode]
	l := &Lease{Holder: holder, Mode: mode, Alias: alias, Allowed: s.allowed(mode), Since: now, Deadline: now.Add(ttl), LastRenewed: now, RenewAfter: s.renewAfter(ttl)}
	l.Until = s.until(holder, now, l.Deadline)
	if mode == Exclusive { // everything stops, nothing answers
		l.Allowed = []string{}
		if err := s.write(l); err != nil {
			return nil, err
		}
		l.Paused = s.pausedServices(s.allUnits())
		s.pause(l)
		Systemctl(ctx, "stop", s.exclusiveUnits())
		l.Ready = true
		return l, s.write(l)
	}
	if err := s.write(l); err != nil {
		return nil, err
	}
	if len(s.units[mode]) > 0 {
		// the units are the lease holder's now (#39): converge must not start them again
		l.Paused = s.pausedServices(s.byMode[mode])
		s.pause(l)
		Systemctl(ctx, "stop", s.units[mode])
	} else if len(leftUnits) > 0 {
		for svc := range leftUnits {
			apply.Resume(s.cfg.Paths, svc)
		}
		Systemctl(ctx, "start", flatten(leftUnits))
	}
	if m.Voice == "cpu" {
		s.voiceDevice("cpu")
	} else if left.Voice == "cpu" {
		s.voiceDevice("gpu")
	}
	l.Ready = true
	return l, s.write(l)
}

// renewAfter is when a holder posts again: a third of its ttl, and at
// most half the grace (Renew), so one missed renewal never loses the card.
func (s *Server) renewAfter(ttl time.Duration) int {
	return int(max(min(ttl/3, s.cfg.Renew/2), time.Second).Seconds())
}

// until is the next renewal deadline: one interval away, never past the
// holder's deadline. The automatic window renews itself per request and
// keeps its idle timeout.
func (s *Server) until(holder string, now, deadline time.Time) time.Time {
	if holder == AutoHolder {
		return deadline
	}
	if u := now.Add(s.cfg.Renew); u.Before(deadline) {
		return u
	}
	return deadline
}

func (s *Server) pausedServices(by map[string][]string) []string {
	return slices.Sorted(maps.Keys(by))
}

// pause writes the marker for every service whose units the lease stopped,
// with the reason the Box page and Haus show the family.
func (s *Server) pause(l *Lease) {
	for _, svc := range l.Paused {
		apply.WritePause(s.cfg.Paths, apply.Pause{Service: svc, By: "lease:" + l.Holder, Until: l.Until.UTC(), Reason: s.reason(l)})
	}
}

// reason says in plain German who has the card and what that costs.
func (s *Server) reason(l *Lease) string {
	what := "exklusiv"
	if l.Mode != Exclusive {
		what = "„" + cmp.Or(l.Alias, s.cfg.Modes[l.Mode].Label, l.Mode) + "“"
	}
	cost := "diese Dienste stehen"
	if s.cfg.Modes[l.Mode].Voice == "cpu" || l.Mode == Exclusive {
		cost = "Sprache läuft langsamer"
	}
	return fmt.Sprintf("Grafikkarte reserviert für %s (%s): %s, bis %s", l.Holder, what, cost, l.Deadline.Local().Format("15:04"))
}

func (s *Server) allUnits() map[string][]string {
	out := map[string][]string{}
	for _, by := range s.byMode {
		for svc, us := range by {
			out[svc] = append(out[svc], us...)
		}
	}
	return out
}

func (s *Server) sortedModes() []string {
	return slices.Sorted(maps.Keys(s.cfg.Modes))
}

// exclusiveUnits is every mode's units, sorted, each once.
func (s *Server) exclusiveUnits() []string {
	return slices.Compact(flatten(s.units))
}

// voiceDevice writes the device file the voice units read as an EnvironmentFile.
func (s *Server) voiceDevice(dev string) {
	env := "WHISPER_DEVICE=cuda\nTTS_DEVICE=cuda\n"
	if dev == "cpu" {
		env = "WHISPER_DEVICE=cpu\nTTS_DEVICE=cpu\n"
	}
	os.WriteFile(filepath.Join(s.cfg.LeaseDir, "voice-device.env"), []byte(env), 0o644)
	Systemctl(context.Background(), "restart", s.voice)
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
	for _, svc := range cur.Paused {
		apply.Resume(s.cfg.Paths, svc)
	}
	if cur.Mode == Exclusive {
		Systemctl(ctx, "start", s.exclusiveUnits())
	} else if m, ok := s.cfg.Modes[cur.Mode]; ok {
		if us := s.units[cur.Mode]; len(us) > 0 {
			Systemctl(ctx, "start", us)
		}
		if m.Voice == "cpu" {
			s.voiceDevice("gpu")
		}
	}
	s.clear()
	return nil
}

func (s *Server) expiryLoop() {
	for {
		time.Sleep(30 * time.Second)
		s.expireOnce(context.Background())
	}
}

// expireOnce releases a lease past its renewal deadline and journals a
// foreign window that has stood longer than WarnAfter — once, yellow, in
// plain German, so the family learns why Sprache is slow (#42).
func (s *Server) expireOnce(ctx context.Context) {
	l := s.read()
	if l == nil {
		return
	}
	if time.Now().After(l.Until) {
		s.Release(ctx, "")
		return
	}
	costs := len(s.units[l.Mode]) > 0 || s.cfg.Modes[l.Mode].Voice == "cpu" || l.Mode == Exclusive
	if !costs || l.Warned || time.Since(l.Since) < s.cfg.WarnAfter {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if l = s.read(); l == nil || l.Warned {
		return
	}
	l.Warned = true
	s.write(l)
	journal.Append(s.cfg.Paths.Journal, journal.Entry{Actor: "lease", Verb: "lease", Service: "llama", Result: "gelb",
		Detail: fmt.Sprintf("%s hält die Grafikkarte seit %d min: %s", l.Holder, int(time.Since(l.Since).Minutes()), s.reason(l))})
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
	reply := func(code int, v map[string]any) {
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(v)
	}
	refuse := func(code int, reason string, more map[string]any) {
		v := map[string]any{"ok": false, "reason": reason}
		maps.Copy(v, more)
		reply(code, v)
	}
	// Reading the lease is open; taking or giving back the card wants the model key (#42).
	if (r.Method == http.MethodPost || r.Method == http.MethodDelete) && !s.authorized(r) {
		refuse(http.StatusUnauthorized, "unauthorized", map[string]any{"message": "the lease API wants the model key: Authorization: Bearer <MODEL_KEY>"})
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	switch r.Method {
	case http.MethodGet:
		reply(http.StatusOK, s.status(r.Context()))
	case http.MethodPost:
		model, ttl, holder, reason := parsePayload(body)
		if reason != "" {
			refuse(http.StatusBadRequest, reason, nil)
			return
		}
		l, err := s.Acquire(r.Context(), holder, model, ttl)
		if errors.Is(err, ErrHeld) {
			refuse(http.StatusConflict, "held", map[string]any{"holder": l.Holder, "expires_at": epoch(l.Deadline), "mode": l.Mode, "allowed": l.Allowed})
			return
		}
		if errors.Is(err, ErrUnknownModel) {
			refuse(http.StatusBadRequest, "invalid_model", map[string]any{"message": err.Error()})
			return
		}
		if err != nil {
			refuse(http.StatusInternalServerError, "internal", map[string]any{"message": err.Error()})
			return
		}
		alias := s.answering(l)
		if alias != "" && !s.warm(r.Context(), alias) {
			// not a failure: the weights are loading; the caller asks GET, it does not post again
			reply(http.StatusAccepted, map[string]any{"ok": true, "state": "preparing", "model": l.Mode, "alias": alias, "retry_after": retryAfter, "expires_at": nil})
			return
		}
		reply(http.StatusOK, map[string]any{"ok": true, "state": "ready", "model": l.Mode, "alias": alias, "expires_at": epoch(l.Deadline), "renew_after": l.RenewAfter, "allowed": l.Allowed})
	case http.MethodDelete:
		holder, reason := parseRelease(body)
		if reason != "" {
			refuse(http.StatusBadRequest, reason, nil)
			return
		}
		if cur := s.read(); cur != nil && holder != "" && cur.Holder != holder {
			refuse(http.StatusConflict, "held", map[string]any{"holder": cur.Holder, "expires_at": epoch(cur.Deadline)})
			return
		}
		// a bodyless DELETE stays the admin's way out of a window nobody renews
		if err := s.Release(r.Context(), holder); err != nil {
			refuse(http.StatusConflict, "held", map[string]any{"message": err.Error()})
			return
		}
		reply(http.StatusOK, map[string]any{"ok": true, "state": "released"})
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
	}
}

// status is what GET answers: the window's state, who holds it, until
// when, and the heartbeat. Laib adds mode and allowed for the proxy's
// clients; a consumer that reads only the contract's fields sees the same.
func (s *Server) status(ctx context.Context) map[string]any {
	out := map[string]any{"state": "none", "model": "", "alias": s.cfg.Household, "expires_at": nil, "holder": "",
		"last_renewed_at": nil, "renew_after": nil, "retry_after": retryAfter}
	l := s.read()
	if l == nil {
		out["allowed"], out["mode"] = s.Policy()
		return out
	}
	alias := s.answering(l)
	out["model"], out["alias"], out["holder"], out["mode"], out["allowed"] = l.Mode, alias, l.Holder, l.Mode, l.Allowed
	out["last_renewed_at"], out["renew_after"] = epoch(l.LastRenewed), l.RenewAfter
	if !l.Ready || (alias != "" && !s.warm(ctx, alias)) {
		out["state"] = "preparing"
		return out
	}
	out["state"], out["expires_at"] = "ready", epoch(l.Deadline)
	return out
}

// answering is the preset that answers in a window: the one asked for,
// else the first the window allows; none in the exclusive window.
func (s *Server) answering(l *Lease) string {
	if l.Alias != "" {
		return l.Alias
	}
	if len(l.Allowed) > 0 {
		return l.Allowed[0]
	}
	return ""
}

// epoch is a contract time: Unix seconds, or null for none.
func epoch(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.Unix()
}

// parsePayload reads {model, ttl_s, holder}, a closed set: an unknown
// field is refused, so a session or round id cannot creep in later. Every
// window has a named holder; the ttl is clamped.
func parsePayload(body []byte) (model string, ttl time.Duration, holder, reason string) {
	var raw map[string]json.RawMessage
	if !json.Valid(body) {
		return "", 0, "", "invalid_json"
	}
	if json.Unmarshal(body, &raw) != nil || raw == nil {
		return "", 0, "", "invalid_payload"
	}
	for k := range raw {
		if k != "model" && k != "ttl_s" && k != "holder" {
			return "", 0, "", "unexpected_field"
		}
	}
	if json.Unmarshal(raw["model"], &model) != nil || strings.TrimSpace(model) == "" {
		return "", 0, "", "invalid_model"
	}
	model = strings.TrimSpace(model)
	secs := ttlDefault
	if v, ok := raw["ttl_s"]; ok {
		if json.Unmarshal(v, &secs) != nil || secs <= 0 {
			return "", 0, "", "invalid_ttl"
		}
	}
	if json.Unmarshal(raw["holder"], &holder) != nil || !holderRe.MatchString(holder) {
		return "", 0, "", "invalid_holder"
	}
	return model, time.Duration(min(max(secs, ttlMin), ttlMax)) * time.Second, holder, ""
}

// parseRelease reads DELETE's optional {holder}; empty is the bodyless call.
func parseRelease(body []byte) (holder, reason string) {
	if len(bytes.TrimSpace(body)) == 0 {
		return "", ""
	}
	var raw map[string]json.RawMessage
	if !json.Valid(body) {
		return "", "invalid_json"
	}
	if json.Unmarshal(body, &raw) != nil || raw == nil {
		return "", "invalid_payload"
	}
	for k := range raw {
		if k != "holder" {
			return "", "unexpected_field"
		}
	}
	if json.Unmarshal(raw["holder"], &holder) != nil || !holderRe.MatchString(holder) {
		return "", "invalid_holder"
	}
	return holder, ""
}

// authorized: the bearer is the model key (the same one consumers send
// the proxy), compared in constant time.
func (s *Server) authorized(r *http.Request) bool {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	tok, ok := strings.CutPrefix(h, "Bearer ")
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(tok)), []byte(s.cfg.Token)) == 1
}

// warm asks the router for one token from the preset; false when it does
// not answer within a few seconds (the weights are still loading).
func (s *Server) warm(ctx context.Context, alias string) bool {
	if _, ok := s.warmed.Load(alias); ok {
		return true
	}
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
	if resp.StatusCode != http.StatusOK {
		return false
	}
	s.warmed.Store(alias, true)
	return true
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
	if model != "" && !slices.Contains(allowed, model) {
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
	if model != "" && model != s.cfg.Household {
		s.renewIfOurs(r.Context()) // a household request must not keep a foreign window alive (#42)
	}
	s.proxy.ServeHTTP(w, r)
}

// modeFollows: from the household, a request for a preset the router
// serves takes the window that allows every preset itself; a window a named holder has
// stands.
func (s *Server) modeFollows(ctx context.Context, want string) bool {
	if !s.knownPreset(want) {
		return false
	}
	if l := s.read(); l != nil {
		return slices.Contains(l.Allowed, want)
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
				m["allowed_in_mode"] = slices.Contains(allowed, id)
				data[i] = m
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(listing)
}
