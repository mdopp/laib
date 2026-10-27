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
// refused with 409 naming what is allowed. The household window (no lease)
// allows the household model only: another window is taken by a named
// holder's lease, never by a request (#522). GET also says when a person
// last asked the model: every forwarded request not marked
// X-Laib-Background (#550).
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
	"log"
	"maps"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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
	// Laib is laib serve as the lease reaches it (ADR 0027): the services
	// and their units, pause and resume; main wires it to MCP, tests to a fake.
	Laib      Box           `json:"-"`
	RouterURL string        `json:"-"`
	EmbedURL  string        `json:"-"` // llama-embed, where /v1/embeddings goes (#567); default 127.0.0.1:11436
	LeaseDir  string        `json:"-"`
	Token     string        `json:"-"` // bearer for POST/DELETE on the lease API (#42)
	Renew     time.Duration `json:"-"` // a holder renews within this, or loses the card
	WarnAfter time.Duration `json:"-"` // a foreign window older than this is noted yellow
}

// Box is what the lease asks of laib (ADR 0027). It stops no unit and
// writes no marker itself: laib's pause verb does both, for the units it
// names, and resume brings back what the pause stopped and restarted.
type Box interface {
	Services(ctx context.Context) ([]Service, error) // laib status: who provides which role, with which units
	Pause(ctx context.Context, h Hold) error
	Resume(ctx context.Context, service string) error
	Note(ctx context.Context, text string) error // a line in laib's journal
}

// Service is one row of laib status.
type Service struct {
	Name     string            `json:"name"`
	Provides []string          `json:"provides"`
	Units    map[string]string `json:"units"`
}

// Hold is one pause: the service, until when and why (the family reads
// it), the units to stop and the ones to restart. A renewal names none.
type Hold struct {
	Service       string
	Until         time.Time
	Reason        string
	Stop, Restart []string
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

// staleRouter is the holder of the automatic window the proxy took itself
// before #522; a lease.json it left is released at start.
const staleRouter = "router"

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

// Server holds the state.
type Server struct {
	cfg      Config
	mu       sync.Mutex
	proxy    *httputil.ReverseProxy
	embed    *httputil.ReverseProxy // llama-embed (#567)
	client   *http.Client
	warmed   sync.Map                       // preset → answered once: GET does not ask the router again
	units    map[string][]string            // mode → resolved unit names, sorted
	byMode   map[string]map[string][]string // mode → service → its units
	voice    map[string][]string            // service → its voice units
	roles    []statusFinding                // the modes' missing roles (#374)
	services []Service                      // laib status, once laib answered
	away     string                         // why laib did not answer; "" once it did (ADR 0027)
	router   string                         // what the router said at the last refresh (#785): its part of the status line
	silent   string                         // why the router did not answer at the last refresh; "" when it did
	human    atomic.Int64                   // Unix seconds of the last person's request (#550)
}

type statusFinding struct {
	Severity string `json:"severity"`
	Detail   string `json:"detail"`
	Fix      string `json:"fix"`
}

// resolve turns role/container refs into (service → unit names). A
// container the providing service does not have is an error, so a stale
// presets.json stops the lease at start, not on the GPU. A role nobody
// provides is left out and returned (#374): no unit of it runs, so there is
// nothing to stop, and one missing service must not take the lease down.
func (s *Server) resolve(refs []string) (map[string][]string, []string, error) {
	out := map[string][]string{}
	var missing []string
	for _, ref := range refs {
		role, container, ok := strings.Cut(ref, "/")
		if !ok || role == "" || container == "" {
			return nil, nil, fmt.Errorf("presets.json: %q is not role/container", ref)
		}
		service, units := "", []string(nil)
		for _, svc := range s.services {
			if slices.Contains(svc.Provides, role) {
				service, units = svc.Name, slices.Sorted(maps.Keys(svc.Units))
			}
		}
		if service == "" {
			missing = append(missing, ref)
			continue
		}
		want := service + "-" + container + ".service"
		if container == service {
			want = service + ".service"
		}
		if !slices.Contains(units, want) {
			return nil, nil, fmt.Errorf("presets.json: %s has no unit %s (%s); it has %s", service, want, ref, strings.Join(units, ", "))
		}
		out[service] = append(out[service], want)
	}
	return out, missing, nil
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
	if cfg.Token == "" {
		return nil, errors.New("the lease token (LEASE_TOKEN) is empty: the lease API would be open to the LAN (#42)")
	}
	if cfg.Renew <= 0 {
		cfg.Renew = 10 * time.Minute
	}
	if cfg.WarnAfter <= 0 {
		cfg.WarnAfter = 20 * time.Minute
	}
	if cfg.Laib == nil {
		return nil, errors.New("no way to laib (LAIB_MCP_URL): the lease could pause nothing")
	}
	u, err := url.Parse(cfg.RouterURL)
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, proxy: httputil.NewSingleHostReverseProxy(u), client: &http.Client{Timeout: 10 * time.Minute}}
	s.proxy.FlushInterval = -1 // token streams
	e, err := url.Parse(cmp.Or(cfg.EmbedURL, "http://127.0.0.1:11436"))
	if err != nil {
		return nil, err
	}
	s.embed = httputil.NewSingleHostReverseProxy(e)
	if err := s.connect(context.Background()); err != nil {
		return nil, err
	}
	if l := s.read(); l != nil && l.Holder == staleRouter {
		s.Release(context.Background(), staleRouter) // the window a request took before #522
	}
	s.router, s.silent = s.measure(context.Background())
	if err := s.writeStatus(s.read()); err != nil {
		return nil, err
	}
	go s.expiryLoop()
	go s.refreshLoop()
	return s, nil
}

// StatusEvery is how often the lease measures the router and writes its
// status again (#785): the Box page's age is the age of a measurement.
const StatusEvery = 15 * time.Minute

func (s *Server) refreshLoop() {
	for {
		time.Sleep(StatusEvery)
		s.Refresh(context.Background())
	}
}

// Refresh asks laib again while it was away, measures the router and
// writes the status file.
func (s *Server) Refresh(ctx context.Context) {
	if s.away != "" {
		s.connect(ctx)
	}
	router, silent := s.measure(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.router, s.silent = router, silent
	s.writeStatus(s.read())
}

// measure asks the router which models it has loaded, in ten seconds: the
// status line's part, and why the router did not answer ("" when it did).
func (s *Server) measure(ctx context.Context) (string, string) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.cfg.RouterURL, "/")+"/v1/models", nil)
	var listing struct {
		Data []struct {
			ID     string `json:"id"`
			Status struct {
				Value string `json:"value"`
			} `json:"status"`
		} `json:"data"`
	}
	resp, err := s.client.Do(req)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			err = fmt.Errorf("HTTP %d", resp.StatusCode)
		} else {
			err = json.NewDecoder(resp.Body).Decode(&listing)
		}
	}
	if err != nil {
		return "Router nicht gemessen", err.Error()
	}
	var loaded []string
	for _, m := range listing.Data {
		if m.Status.Value == "loaded" {
			loaded = append(loaded, m.ID)
		}
	}
	if len(loaded) == 0 {
		return "kein Modell geladen, das erste lädt bei der nächsten Frage", ""
	}
	return "geladen: " + strings.Join(loaded, ", "), ""
}

// connect asks laib for its services and resolves the modes against them.
// While laib does not answer the lease serves the household and says so
// (ADR 0027); a ref the answering laib does not have stops it, as before.
func (s *Server) connect(ctx context.Context) error {
	svcs, err := s.cfg.Laib.Services(ctx)
	if err != nil {
		s.away = err.Error()
		return nil
	}
	s.services, s.away, s.roles = svcs, "", nil
	s.units, s.byMode = map[string][]string{}, map[string]map[string][]string{}
	fix := func(ref string) string {
		return "einen Dienst mit der Rolle „" + strings.Split(ref, "/")[0] + "“ installieren, oder " + ref + " aus presets.json nehmen"
	}
	for _, name := range slices.Sorted(maps.Keys(s.cfg.Modes)) {
		by, missing, err := s.resolve(s.cfg.Modes[name].Stops)
		if err != nil {
			return fmt.Errorf("mode %s: %w", name, err)
		}
		for _, ref := range missing {
			s.roles = append(s.roles, statusFinding{"gelb", fmt.Sprintf("Modus %s: %s stellt kein Dienst bereit; die Lease hält es nicht an", name, ref), fix(ref)})
		}
		s.byMode[name] = by
		s.units[name] = flatten(by)
	}
	voice, missing, err := s.resolve(s.cfg.VoiceUnits)
	if err != nil {
		return fmt.Errorf("voiceUnits: %w", err)
	}
	for _, ref := range missing {
		s.roles = append(s.roles, statusFinding{"gelb", ref + " stellt kein Dienst bereit; ein Wechsel des Sprachgeräts startet es nicht neu", fix(ref)})
	}
	s.voice = voice
	return nil
}

// reach is connect again while laib was away; an error says the lease can
// stop nothing now.
func (s *Server) reach(ctx context.Context) error {
	if s.away != "" {
		if err := s.connect(ctx); err != nil {
			return err
		}
	}
	if s.away != "" {
		return fmt.Errorf("Laib antwortet nicht (%s): die Lease kann gerade nichts anhalten, nur der Haushalt läuft", s.away)
	}
	return nil
}

// writeStatus writes the status file laib status and diag read
// (templates/llama status.file): the modes' missing roles (#374), and a
// standing lease in yellow with its holder, window and end (#522), so a
// mode other than the household's is never silent.
func (s *Server) writeStatus(l *Lease) error {
	findings := append([]statusFinding{}, s.roles...)
	if s.away != "" {
		findings = append(findings, statusFinding{"gelb", "Laib antwortet nicht: die Lease kann keinen Dienst anhalten, ein Fenster mit Pausen wird abgelehnt (" + s.away + ")", "systemctl --user status laib.service; die Lease fragt bei jedem Fenster neu"})
	}
	detail := "Haushalt: nur " + s.cfg.Household
	if l != nil {
		detail = "Modus " + l.Mode + " für " + l.Holder
		cost := s.cfg.Household + " lädt danach neu"
		if len(l.Paused) > 0 {
			cost = "angehalten: " + strings.Join(l.Paused, ", ") + "; " + cost
		}
		findings = append(findings, statusFinding{"gelb",
			fmt.Sprintf("Modus %s statt Haushalt: %s hält die Grafikkarte seit %s für „%s“ (%s)", l.Mode, l.Holder, l.Since.Local().Format("15:04"), cmp.Or(s.answering(l), l.Mode), cost),
			fmt.Sprintf("endet um %s, wenn %s nicht erneuert; sofort: DELETE /api/model-lease mit dem Modellschlüssel", l.Until.Local().Format("15:04"), l.Holder)})
	}
	if len(s.roles) > 0 {
		detail += fmt.Sprintf("; %d Rolle(n) der Modi fehlen", len(s.roles))
	}
	if s.router != "" {
		detail += " · " + s.router
	}
	if s.silent != "" {
		findings = append(findings, statusFinding{"gelb", "Der Modell-Router antwortet nicht (" + s.silent + "): ob ein Modell antwortet, ist offen", "laib logs llama; laib restart llama"})
	}
	b, _ := json.MarshalIndent(map[string]any{"at": time.Now().UTC().Format(time.RFC3339), "detail": detail, "findings": findings}, "", "  ")
	os.MkdirAll(s.cfg.LeaseDir, 0o755)
	return writeAtomic(filepath.Join(s.cfg.LeaseDir, "laib-status.json.tmp"), filepath.Join(s.cfg.LeaseDir, "laib-status.json"), b, 0o644)
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
	if err := writeAtomic(s.leasePath()+".tmp", s.leasePath(), b, 0o644); err != nil {
		return err
	}
	return s.writeStatus(l)
}

func (s *Server) clear() {
	os.Remove(s.leasePath())
	s.writeStatus(nil)
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
	if cur != nil && cur.Holder != holder {
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
		if cur.Deadline.IsZero() {
			cur.Deadline = now.Add(ttl)
		}
		cur.Until = s.until(now, cur.Deadline)
		cur.LastRenewed, cur.RenewAfter = now, s.renewAfter(ttl)
		if alias != "" {
			cur.Alias = alias
		}
		for _, svc := range cur.Paused { // the pauses move with it, their units stay as they are
			if err := s.cfg.Laib.Pause(ctx, Hold{Service: svc, Until: cur.Until, Reason: s.reason(cur)}); err != nil {
				log.Printf("laib-lease: renewing the pause of %s: %v", svc, err)
			}
		}
		return cur, s.write(cur)
	}
	if m := s.cfg.Modes[mode]; len(m.Stops) > 0 || m.Voice == "cpu" || mode == Exclusive {
		if err := s.reach(ctx); err != nil {
			return nil, err // the card is never taken from services that keep running
		}
	}
	holds := s.holds(mode)
	if cur != nil {
		s.giveBack(ctx, cur) // the holder's other window first
	}
	l := &Lease{Holder: holder, Mode: mode, Alias: alias, Allowed: s.allowed(mode), Since: now, Deadline: now.Add(ttl), LastRenewed: now, RenewAfter: s.renewAfter(ttl)}
	l.Until = s.until(now, l.Deadline)
	if mode == Exclusive { // everything stops, nothing answers
		l.Allowed = []string{}
	}
	if err := s.write(l); err != nil {
		return nil, err
	}
	if s.cfg.Modes[mode].Voice == "cpu" {
		s.voiceDevice("cpu") // before the voice units restart and read it
	}
	// the units are the lease holder's now (#39): laib pauses each service,
	// so converge does not start them again, and stops what the window takes
	for _, svc := range slices.Sorted(maps.Keys(holds)) {
		h := holds[svc]
		h.Until, h.Reason = l.Until, s.reason(l)
		if err := s.cfg.Laib.Pause(ctx, h); err != nil {
			s.giveBack(ctx, l)
			s.clear()
			return nil, fmt.Errorf("laib hält %s nicht an: %w; die Lease gibt die Grafikkarte nicht her", svc, err)
		}
		l.Paused = append(l.Paused, svc)
	}
	l.Ready = true
	return l, s.write(l)
}

// holds is what a window asks laib to pause: per service the units it
// stops, and for the voice on the CPU the voice units to restart. The
// exclusive window stops every window's units.
func (s *Server) holds(mode string) map[string]Hold {
	out := map[string]Hold{}
	by := s.byMode[mode]
	if mode == Exclusive {
		by = s.allUnits()
	}
	for svc, us := range by {
		out[svc] = Hold{Service: svc, Stop: slices.Compact(slices.Sorted(slices.Values(us)))}
	}
	if s.cfg.Modes[mode].Voice == "cpu" {
		for svc, us := range s.voice {
			h := out[svc]
			h.Service, h.Restart = svc, slices.Sorted(slices.Values(us))
			out[svc] = h
		}
	}
	return out
}

// giveBack is the end of a window: the voice back on the GPU first, then
// laib resumes every service the window paused, which starts and restarts
// their units again. A lease.json from before #624 names no pauses: what
// its mode holds is resumed.
func (s *Server) giveBack(ctx context.Context, l *Lease) {
	if s.cfg.Modes[l.Mode].Voice == "cpu" {
		s.voiceDevice("gpu")
	}
	paused := slices.Clone(l.Paused)
	if len(paused) == 0 {
		paused = slices.Sorted(maps.Keys(s.holds(l.Mode)))
	}
	for _, svc := range paused {
		if err := s.cfg.Laib.Resume(ctx, svc); err != nil {
			log.Printf("laib-lease: resume %s: %v (its pause ends at %s, then converge starts it)", svc, err, l.Until.Local().Format("15:04"))
		}
	}
}

// renewAfter is when a holder posts again: a third of its ttl, and at
// most half the grace (Renew), so one missed renewal never loses the card.
func (s *Server) renewAfter(ttl time.Duration) int {
	return int(max(min(ttl/3, s.cfg.Renew/2), time.Second).Seconds())
}

// until is the next renewal deadline: one interval away, never past the
// holder's deadline.
func (s *Server) until(now, deadline time.Time) time.Time {
	if u := now.Add(s.cfg.Renew); u.Before(deadline) {
		return u
	}
	return deadline
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

// voiceDevice writes the device file the voice units read as an
// EnvironmentFile; laib restarts them (Hold.Restart, resume).
func (s *Server) voiceDevice(dev string) {
	env := "WHISPER_DEVICE=cuda\nTTS_DEVICE=cuda\n"
	if dev == "cpu" {
		env = "WHISPER_DEVICE=cpu\nTTS_DEVICE=cpu\n"
	}
	os.MkdirAll(s.cfg.LeaseDir, 0o755)
	os.WriteFile(filepath.Join(s.cfg.LeaseDir, "voice-device.env"), []byte(env), 0o644)
}

// Release gives the card back: voice to gpu, laib resumes what the window
// paused, the lease goes last.
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
	s.reach(ctx)
	s.giveBack(ctx, cur)
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
	s.cfg.Laib.Note(ctx, fmt.Sprintf("%s hält die Grafikkarte seit %d min: %s", l.Holder, int(time.Since(l.Since).Minutes()), s.reason(l)))
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
		"last_renewed_at": nil, "renew_after": nil, "retry_after": retryAfter, "last_human_at": nil}
	if t := s.human.Load(); t != 0 {
		out["last_human_at"] = t
	}
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
	if r.URL.Path == "/v1/embeddings" || r.URL.Path == "/embeddings" {
		s.embeddings(w, r)
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
		// no window opens by request (#522): one call for a foreign preset
		// evicted the household model and put the voice on the CPU for 45 min
		holder := ""
		if l := s.read(); l != nil {
			holder = l.Holder
		}
		w.Header().Set("Content-Type", "application/json")
		code := http.StatusConflict
		if !s.knownPreset(model) {
			code = http.StatusNotFound // a raw file id or a typo: no window ever allows it (#395)
		}
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(s.denial(model, mode, allowed, holder))
		return
	}
	// a service's own work says so (the Operator's, #550); the rest is a person's
	if r.Header.Get("X-Laib-Background") == "" {
		s.human.Store(time.Now().Unix())
	}
	s.proxy.ServeHTTP(w, r)
}

// embeddings go to llama-embed, so a pod reaches it through this proxy
// and its own port stays on the host's loopback (#567, owner 2026-10-24).
// The router knows no embedding preset, so the model is not checked; a
// window that stops model/embed refuses while it stands. An embedding is
// indexing, never a person asking (#550).
func (s *Server) embeddings(w http.ResponseWriter, r *http.Request) {
	if l := s.read(); l != nil && (l.Mode == Exclusive || slices.Contains(s.cfg.Modes[l.Mode].Stops, "model/embed")) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "Die Einbettungen ruhen: " + s.reason(l) + ". Danach wieder versuchen."}})
		return
	}
	s.embed.ServeHTTP(w, r)
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
		if holder == "" {
			say = "Im Haushalt antwortet nur das Haushaltsmodell " + s.cfg.Household + "."
			remedy = "Ein anderes Modell braucht eine Lease: POST /api/model-lease {model, holder}."
		} else {
			remedy = "„" + holder + "“ hat die Grafikkarte gerade; die Lease zeigt, bis wann."
		}
	}
	e := map[string]any{"message": fmt.Sprintf("Modell %s ist im Modus %s nicht erlaubt. %s %s", model, mode, say, remedy), "mode": mode, "allowed": allowed}
	if !s.knownPreset(model) {
		e["code"] = "model_not_found"
	}
	return map[string]any{"error": e}
}

// catalogue lists the presets, the household's first, marked
// allowed_in_mode, with what the router says about each. Nothing else: the
// router also lists every GGUF file in its models dir, and a client that
// picks one of those is refused (#395).
func (s *Server) catalogue(w http.ResponseWriter, r *http.Request) {
	allowed, mode := s.Policy()
	router := map[string]map[string]any{}
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, strings.TrimRight(s.cfg.RouterURL, "/")+"/v1/models", nil)
	if resp, err := s.client.Do(req); err == nil {
		defer resp.Body.Close()
		var listing struct {
			Data []map[string]any `json:"data"`
		}
		json.NewDecoder(resp.Body).Decode(&listing)
		for _, m := range listing.Data {
			if id, _ := m["id"].(string); id != "" {
				router[id] = m
			}
		}
	}
	presets := []string{s.cfg.Household}
	for _, p := range s.cfg.Presets {
		if p.Alias != s.cfg.Household {
			presets = append(presets, p.Alias)
		}
	}
	data := []any{}
	for _, id := range presets {
		m := router[id]
		if m == nil {
			m = map[string]any{"object": "model"}
		}
		m["id"], m["allowed_in_mode"] = id, slices.Contains(allowed, id)
		data = append(data, m)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data, "mode": mode})
}
