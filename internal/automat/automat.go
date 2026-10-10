// Package automat is the Bote's hand for Home Assistant's configuration:
// automations are read, written and deleted over HA's REST API
// (/api/config/automation/config/<id>), which HA's own MCP server does not
// offer (#26). Writes are meant to run behind Hermes' `trust: untrusted`,
// so the admin confirms each one on Signal.
package automat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/mcpserver"
	"gopkg.in/yaml.v3"
)

// Config is where Home Assistant is and how to talk to it.
type Config struct {
	HAURL string // http://host.containers.internal:8123
	Token string // long-lived access token
}

type client struct {
	cfg  Config
	http *http.Client
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
		Instructions: "Automationen in Home Assistant lesen, anlegen, ändern, löschen. Vor dem Schreiben: Entitäten mit `entitaeten` nachschlagen, die Automation als YAML zeigen und bestätigen lassen. Die id ist ein Slug (flur_licht_aus). Jede geschriebene Automation trägt 'laib:' in der Beschreibung.",
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
					b, code, err := c.do(ctx, "POST", "/api/config/automation/config/"+url.PathEscape(id), cfg)
					if err != nil {
						return "", err
					}
					if code != 200 {
						return "", fmt.Errorf("home assistant: %d %s", code, string(b))
					}
					return fmt.Sprintf("geschrieben: %s (%s) — automation.%s", alias, id, id), nil
				}},
			{Name: "automation_loeschen", Description: "Automation löschen (id). Bestätigen lassen.",
				Schema: obj(map[string]any{"id": map[string]any{"type": "string"}}, "id"),
				Call: func(ctx context.Context, a map[string]any) (string, error) {
					id := str(a, "id")
					if !idRe.MatchString(id) {
						return "", fmt.Errorf("id %q: nur a-z, 0-9, _", id)
					}
					b, code, err := c.do(ctx, "DELETE", "/api/config/automation/config/"+url.PathEscape(id), nil)
					if err != nil {
						return "", err
					}
					if code != 200 {
						return "", fmt.Errorf("home assistant: %d %s", code, string(b))
					}
					return "gelöscht: " + id, nil
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
