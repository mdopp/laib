// Package automat is the Bote's hand for Home Assistant's configuration:
// automations are read, written and deleted over HA's REST API
// (/api/config/automation/config/<id>), which HA's own MCP server does not
// offer (#26). Writes are meant to run behind Hermes' `trust: untrusted`,
// so the admin confirms each one on Signal.
package automat

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mdopp/laib/internal/mcpserver"
	"gopkg.in/yaml.v3"
)

// Config is where Home Assistant is and how to talk to it.
type Config struct {
	HAURL string // http://host.containers.internal:8123
	Token string // long-lived access token
	Data  string // where deleted automations are kept for restore (#48)
}

type client struct {
	cfg  Config
	http *http.Client

	mu      sync.Mutex
	pending map[string]pendingChange // confirmation code → what will happen
}

// pendingChange is a write or a delete waiting for its code (#48): the
// model shows it, the person confirms, only then does Home Assistant change.
type pendingChange struct {
	Kind    string // write | delete
	ID      string
	Alias   string
	Config  map[string]any
	Created time.Time
}

// newCode is four digits the person repeats back.
func newCode() string {
	var b [2]byte
	rand.Read(b[:])
	return fmt.Sprintf("%04d", (int(b[0])<<8|int(b[1]))%10000)
}

func (c *client) keep(kind, id, alias string, cfg map[string]any) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending == nil {
		c.pending = map[string]pendingChange{}
	}
	for code, p := range c.pending {
		if time.Since(p.Created) > 10*time.Minute {
			delete(c.pending, code)
		}
	}
	code := newCode()
	for c.pending[code].ID != "" {
		code = newCode()
	}
	c.pending[code] = pendingChange{Kind: kind, ID: id, Alias: alias, Config: cfg, Created: time.Now()}
	return code
}

func (c *client) take(code string) (pendingChange, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.pending[code]
	if ok {
		delete(c.pending, code)
	}
	if ok && time.Since(p.Created) > 10*time.Minute {
		return pendingChange{}, false
	}
	return p, ok
}

// trash keeps a deleted automation's config for restore.
func (c *client) trash(id string, cfg map[string]any) error {
	if c.cfg.Data == "" {
		return nil
	}
	dir := filepath.Join(c.cfg.Data, "geloescht")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	y, _ := yaml.Marshal(cfg)
	return os.WriteFile(filepath.Join(dir, id+".yaml"), y, 0o644)
}

func (c *client) do(ctx context.Context, method, path string, body any) ([]byte, int, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.cfg.HAURL, "/")+path, rd)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return out, resp.StatusCode, nil
}

var idRe = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

// Slug turns a sentence into an automation id: "Flur Licht aus" → flur_licht_aus.
func Slug(s string) string {
	s = strings.ToLower(s)
	s = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss").Replace(s)
	var b strings.Builder
	last := '_'
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			last = r
		default:
			if last != '_' {
				b.WriteRune('_')
				last = '_'
			}
		}
	}
	return strings.Trim(b.String(), "_")
}

// state is one row of /api/states.
type state struct {
	EntityID   string         `json:"entity_id"`
	State      string         `json:"state"`
	Attributes map[string]any `json:"attributes"`
}

func (c *client) states(ctx context.Context) ([]state, error) {
	b, code, err := c.do(ctx, "GET", "/api/states", nil)
	if err != nil {
		return nil, err
	}
	if code != 200 {
		return nil, fmt.Errorf("home assistant: /api/states → %d", code)
	}
	var out []state
	return out, json.Unmarshal(b, &out)
}

// Server builds the MCP endpoint.
func Server(cfg Config, version string) *mcpserver.Server {
	c := &client{cfg: cfg, http: &http.Client{Timeout: 20 * time.Second}}
	str := mcpserver.Str
	obj := func(props map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": props, "required": required}
	}
	return &mcpserver.Server{
		Name: "automat", Version: version,
		Instructions: "Automationen in Home Assistant lesen, anlegen, ändern, löschen. Schreiben und Löschen passieren nie sofort: die Antwort nennt einen vierstelligen Code; zeige der Person, was passieren würde, und rufe automation_bestaetigen erst, wenn sie den Code genannt hat. Die id ist ein Slug (flur_licht_aus). Jede geschriebene Automation trägt 'laib:' in der Beschreibung.",
		Tools: []mcpserver.Tool{
			{Name: "entitaeten", Description: "Entitäten von Home Assistant: id, Name, Zustand. Filter: domain (light, switch, media_player, binary_sensor, person …) und/oder suche (Teil des Namens oder der id).",
				Schema: obj(map[string]any{"domain": map[string]any{"type": "string"}, "suche": map[string]any{"type": "string"}}),
				Call: func(ctx context.Context, a map[string]any) (string, error) {
					sts, err := c.states(ctx)
					if err != nil {
						return "", err
					}
					dom, q := str(a, "domain"), strings.ToLower(str(a, "suche"))
					var lines []string
					for _, s := range sts {
						if dom != "" && !strings.HasPrefix(s.EntityID, dom+".") {
							continue
						}
						name, _ := s.Attributes["friendly_name"].(string)
						if q != "" && !strings.Contains(strings.ToLower(s.EntityID+" "+name), q) {
							continue
						}
						lines = append(lines, fmt.Sprintf("%s · %s · %s", s.EntityID, name, s.State))
					}
					sort.Strings(lines)
					if len(lines) > 200 {
						lines = append(lines[:200], fmt.Sprintf("… %d weitere, enger filtern", len(lines)-200))
					}
					if len(lines) == 0 {
						return "keine Entität passt", nil
					}
					return strings.Join(lines, "\n"), nil
				}},
			{Name: "automationen", Description: "Alle Automationen: id, Name, an/aus, zuletzt ausgelöst.",
				Schema: obj(map[string]any{}),
				Call: func(ctx context.Context, a map[string]any) (string, error) {
					sts, err := c.states(ctx)
					if err != nil {
						return "", err
					}
					var lines []string
					for _, s := range sts {
						if !strings.HasPrefix(s.EntityID, "automation.") {
							continue
						}
						id, _ := s.Attributes["id"].(string)
						name, _ := s.Attributes["friendly_name"].(string)
						last, _ := s.Attributes["last_triggered"].(string)
						lines = append(lines, fmt.Sprintf("%s · %s · %s · zuletzt %s", id, name, s.State, orDash(last)))
					}
					sort.Strings(lines)
					if len(lines) == 0 {
						return "keine Automationen", nil
					}
					return strings.Join(lines, "\n"), nil
				}},
			{Name: "automation_lesen", Description: "Eine Automation als YAML (id aus `automationen`).",
				Schema: obj(map[string]any{"id": map[string]any{"type": "string"}}, "id"),
				Call: func(ctx context.Context, a map[string]any) (string, error) {
					id := str(a, "id")
					if !idRe.MatchString(id) {
						return "", fmt.Errorf("id %q: nur a-z, 0-9, _", id)
					}
					b, code, err := c.do(ctx, "GET", "/api/config/automation/config/"+url.PathEscape(id), nil)
					if err != nil {
						return "", err
					}
					if code == 404 {
						return "", fmt.Errorf("keine Automation %s", id)
					}
					if code != 200 {
						return "", fmt.Errorf("home assistant: %d %s", code, string(b))
					}
					var cfg map[string]any
					json.Unmarshal(b, &cfg)
					y, _ := yaml.Marshal(cfg)
					return string(y), nil
				}},
			{Name: "automation_schreiben", Description: "Automation anlegen oder ersetzen. yaml: alias, description, trigger(s), condition(s), action(s) wie in HAs Automations-YAML. Erst zeigen und bestätigen lassen; HA lädt sofort neu.",
				Schema: obj(map[string]any{"id": map[string]any{"type": "string", "description": "Slug, z.B. flur_licht_aus; leer = aus dem alias gebildet"}, "yaml": map[string]any{"type": "string"}}, "yaml"),
				Call: func(ctx context.Context, a map[string]any) (string, error) {
					var cfg map[string]any
					if err := yaml.Unmarshal([]byte(str(a, "yaml")), &cfg); err != nil {
						return "", fmt.Errorf("yaml: %w", err)
					}
					alias, _ := cfg["alias"].(string)
					id := str(a, "id")
					if id == "" {
						id = Slug(alias)
					}
					if !idRe.MatchString(id) {
						return "", fmt.Errorf("id %q: nur a-z, 0-9, _ (alias oder id angeben)", id)
					}
					if alias == "" {
						return "", fmt.Errorf("alias fehlt")
					}
					hasTrigger := cfg["trigger"] != nil || cfg["triggers"] != nil
					hasAction := cfg["action"] != nil || cfg["actions"] != nil
					if !hasTrigger || !hasAction {
						return "", fmt.Errorf("eine Automation braucht trigger(s) und action(s)")
					}
					desc, _ := cfg["description"].(string)
					if !strings.Contains(desc, "laib:") {
						cfg["description"] = strings.TrimSpace("laib: über den Boten angelegt. " + desc)
					}
					cfg["id"] = id
					code := c.keep("write", id, alias, cfg)
					y, _ := yaml.Marshal(cfg)
					return fmt.Sprintf("Vorgemerkt, noch nicht geschrieben: %s (%s). Das wird angelegt:\n%s\nZum Ausführen den Code %s bestätigen (automation_bestaetigen). Gilt zehn Minuten.", alias, id, string(y), code), nil
				}},
			{Name: "automation_loeschen", Description: "Automation löschen (id). Wird erst mit dem Code ausgeführt, den diese Antwort nennt; die alte Automation bleibt zum Wiederherstellen aufgehoben.",
				Schema: obj(map[string]any{"id": map[string]any{"type": "string"}}, "id"),
				Call: func(ctx context.Context, a map[string]any) (string, error) {
					id := str(a, "id")
					if !idRe.MatchString(id) {
						return "", fmt.Errorf("id %q: nur a-z, 0-9, _", id)
					}
					b, code, err := c.do(ctx, "GET", "/api/config/automation/config/"+url.PathEscape(id), nil)
					if err != nil {
						return "", err
					}
					if code == 404 {
						return "", fmt.Errorf("keine Automation %s", id)
					}
					var cfg map[string]any
					json.Unmarshal(b, &cfg)
					alias, _ := cfg["alias"].(string)
					conf := c.keep("delete", id, alias, cfg)
					return fmt.Sprintf("Vorgemerkt, noch nicht gelöscht: %s (%s). Zum Löschen den Code %s bestätigen (automation_bestaetigen); automation_wiederherstellen %s holt sie danach zurück.", alias, id, conf, id), nil
				}},
			{Name: "automation_bestaetigen", Description: "Führt eine vorgemerkte Änderung aus: der vierstellige Code aus der Antwort von automation_schreiben oder automation_loeschen, wie die Person ihn bestätigt hat.",
				Schema: obj(map[string]any{"code": map[string]any{"type": "string"}}, "code"),
				Call: func(ctx context.Context, a map[string]any) (string, error) {
					p, ok := c.take(str(a, "code"))
					if !ok {
						return "", fmt.Errorf("kein vorgemerkter Vorgang mit diesem Code (oder älter als zehn Minuten)")
					}
					switch p.Kind {
					case "write":
						b, code, err := c.do(ctx, "POST", "/api/config/automation/config/"+url.PathEscape(p.ID), p.Config)
						if err != nil {
							return "", err
						}
						if code != 200 {
							return "", fmt.Errorf("home assistant: %d %s", code, string(b))
						}
						return fmt.Sprintf("geschrieben: %s (%s) — automation.%s", p.Alias, p.ID, p.ID), nil
					case "delete":
						if err := c.trash(p.ID, p.Config); err != nil {
							return "", fmt.Errorf("aufheben vor dem Löschen: %w", err)
						}
						b, code, err := c.do(ctx, "DELETE", "/api/config/automation/config/"+url.PathEscape(p.ID), nil)
						if err != nil {
							return "", err
						}
						if code != 200 {
							return "", fmt.Errorf("home assistant: %d %s", code, string(b))
						}
						return fmt.Sprintf("gelöscht: %s (%s); automation_wiederherstellen %s holt sie zurück", p.Alias, p.ID, p.ID), nil
					}
					return "", fmt.Errorf("unbekannter Vorgang")
				}},
			{Name: "automation_wiederherstellen", Description: "Holt eine gelöschte Automation zurück (id).",
				Schema: obj(map[string]any{"id": map[string]any{"type": "string"}}, "id"),
				Call: func(ctx context.Context, a map[string]any) (string, error) {
					id := str(a, "id")
					if !idRe.MatchString(id) || c.cfg.Data == "" {
						return "", fmt.Errorf("id %q: nur a-z, 0-9, _", id)
					}
					y, err := os.ReadFile(filepath.Join(c.cfg.Data, "geloescht", id+".yaml"))
					if err != nil {
						return "", fmt.Errorf("nichts Aufgehobenes für %s", id)
					}
					var cfg map[string]any
					if err := yaml.Unmarshal(y, &cfg); err != nil {
						return "", err
					}
					b, code, err := c.do(ctx, "POST", "/api/config/automation/config/"+url.PathEscape(id), cfg)
					if err != nil {
						return "", err
					}
					if code != 200 {
						return "", fmt.Errorf("home assistant: %d %s", code, string(b))
					}
					return "wiederhergestellt: " + id, nil
				}},
			{Name: "automation_schalten", Description: "Automation an- oder ausschalten (entity_id automation.<id>, an: true/false).",
				Schema: obj(map[string]any{"id": map[string]any{"type": "string"}, "an": map[string]any{"type": "boolean"}}, "id", "an"),
				Call: func(ctx context.Context, a map[string]any) (string, error) {
					id := str(a, "id")
					on, _ := a["an"].(bool)
					svc := "turn_off"
					if on {
						svc = "turn_on"
					}
					b, code, err := c.do(ctx, "POST", "/api/services/automation/"+svc, map[string]any{"entity_id": "automation." + id})
					if err != nil {
						return "", err
					}
					if code != 200 {
						return "", fmt.Errorf("home assistant: %d %s", code, string(b))
					}
					return fmt.Sprintf("automation.%s: %s", id, svc), nil
				}},
		},
	}
}

func orDash(s string) string {
	if s == "" {
		return "nie"
	}
	return s
}
