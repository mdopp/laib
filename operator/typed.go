package operator

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// The typed surface (#53): a small local model does not get the verb table
// with one free-text `args` per tool; it gets a handful of task-shaped tools
// with typed parameters — those whose verb the running skill is granted
// (ADR 0023) — and code turns a call into the CLI arguments laib's MCP takes. approve, reject, restore, rollback,
// converge, template, adopt, apply, remove are in no menu, ever.

type typedParam struct {
	name, typ, desc string
	enum            []string
	required        bool
}

type typedTool struct {
	name, desc, mcp string
	params          []typedParam
	// cli builds the arguments string from validated parameters.
	cli func(a map[string]any) string
}

var typedTools = []typedTool{
	{"status", "Zustand der Box und aller Dienste in einer Zeile pro Dienst.", "laib_status", nil,
		func(a map[string]any) string { return "" }},
	{"diagnose", "Befunde mit Schwere und Fix je Dienst, oder für die ganze Box ohne service.", "laib_diag",
		[]typedParam{{name: "service", typ: "string", desc: "nur dieser Dienst; leer = die ganze Box"}},
		func(a map[string]any) string { return str(a, "service") }},
	{"logs", "Die Logzeilen eines Dienstes, Secrets geschwärzt.", "laib_logs",
		[]typedParam{{name: "service", typ: "string", required: true}, {name: "minutes", typ: "integer", desc: "wie weit zurück, 1 bis 1440; Standard 60"}},
		func(a map[string]any) string {
			return str(a, "service") + " --since " + fmt.Sprint(num(a, "minutes", 60)) + "m"
		}},
	{"inspect", "Units, Container, Dateien, Health, letzte Logs eines Dienstes — die erste Minute jeder Diagnose.", "laib_inspect",
		[]typedParam{{name: "service", typ: "string", required: true}},
		func(a map[string]any) string { return str(a, "service") }},
	{"restart", "Units eines Dienstes neu starten und prüfen; Laib macht Snapshot und Journal.", "laib_restart",
		[]typedParam{{name: "service", typ: "string", required: true}},
		func(a map[string]any) string { return str(a, "service") }},
	{"set_value", "Einen Wert in values.yml eines Dienstes ändern — als Vorschlag mit Begründung; Laib wendet an und rollt bei Rot zurück.", "laib_propose",
		[]typedParam{{name: "service", typ: "string", required: true}, {name: "key", typ: "string", desc: "der Schlüssel in values.yml, z. B. IMAGE", required: true}, {name: "value", typ: "string", required: true}, {name: "reason", typ: "string", desc: "ein Satz: warum", required: true}},
		func(a map[string]any) string {
			return quote(str(a, "reason")) + " --set services/" + str(a, "service") + "/values.yml:" + str(a, "key") + "=" + str(a, "value")
		}},
	{"journal", "Was in den letzten Stunden passiert ist.", "laib_journal",
		[]typedParam{{name: "hours", typ: "integer", desc: "1 bis 168; Standard 24"}},
		func(a map[string]any) string { return "--since " + fmt.Sprint(num(a, "hours", 24)) + "h" }},
	{"escalate", "An die nächste Hand übergeben: Kontextbündel plus rote Journalzeile.", "laib_escalate",
		[]typedParam{{name: "service", typ: "string", required: true}, {name: "summary", typ: "string", desc: "was du weißt, ein bis drei Sätze", required: true}},
		func(a map[string]any) string { return str(a, "service") + " " + quote(str(a, "summary")) }},
	{"note", "Zum Schluss immer: was du gesehen und getan hast. incident nach Reparatur oder Eskalation, watch wenn alles in Ordnung war, report für den Morgenbericht.", "laib_note",
		[]typedParam{{name: "kind", typ: "string", enum: []string{"watch", "incident", "report"}, required: true}, {name: "text", typ: "string", desc: "deutsch, drei bis fünf Sätze, Fakten mit Uhrzeit, rot zuerst", required: true}},
		func(a map[string]any) string { return "--kind " + str(a, "kind") + " " + quote(str(a, "text")) }},
}

var serviceName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)

// typedCallText: a reply that starts with a menu name and looks like a call
// written as text ("restart whoami", "restart(service=whoami)").
var typedCallText = regexp.MustCompile(`(?i)^\s*(status|diagnose|logs|inspect|restart|set_value|journal|escalate|note)\s*[(\s:]`)

func looksLikeTypedCall(text string) bool {
	return typedCallText.MatchString(text) && len(firstLine(text)) < 200
}

// statusLine matches laib status's per-service lines: "  grün  adguard".
var statusLine = regexp.MustCompile(`(?m)^\s*(grün|gelb|rot|pausiert)\s+([a-z0-9][a-z0-9-]*)\b`)

// ServicesFromStatus reads the installed services off laib status, for the
// menu's service enum: the MCP answers JSON ({"services": [{"name": …}]}),
// the CLI one text line per service.
func ServicesFromStatus(text string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	var st struct {
		Services []struct {
			Name string `json:"name"`
		} `json:"services"`
	}
	if i := strings.Index(text, "{"); i >= 0 && json.Unmarshal([]byte(text[i:]), &st) == nil && len(st.Services) > 0 {
		for _, s := range st.Services {
			add(s.Name)
		}
	} else {
		for _, m := range statusLine.FindAllStringSubmatch(text, -1) {
			add(m[2])
		}
	}
	sort.Strings(out)
	return out
}

var valueKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,60}$`)

func str(a map[string]any, k string) string { v, _ := a[k].(string); return strings.TrimSpace(v) }

func num(a map[string]any, k string, def int) int {
	if v, ok := a[k].(float64); ok {
		return int(v)
	}
	return def
}

// quote: one CLI argument in double quotes; inner double quotes become
// single ones (the CLI splits on them, the text keeps its sense).
func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `'`) + `"` }

// isTypedTool: a name on the typed menu, offered or not.
func isTypedTool(name string) bool {
	return slices.ContainsFunc(typedTools, func(t typedTool) bool { return t.name == name })
}

// TypedDefs is the menu for a session: the typed tools whose verb the
// skill is granted (has is keyed by MCP tool name, laib_<verb>).
func TypedDefs(services []string, has map[string]bool) []ToolDef {
	var out []ToolDef
	for _, t := range typedTools {
		if !has[t.mcp] {
			continue
		}
		props := map[string]any{}
		var required []string
		for _, p := range t.params {
			prop := map[string]any{"type": p.typ}
			if p.desc != "" {
				prop["description"] = p.desc
			}
			if p.name == "service" && len(services) > 0 {
				prop["enum"] = append([]string(nil), services...)
			}
			if len(p.enum) > 0 {
				prop["enum"] = p.enum
			}
			props[p.name] = prop
			if p.required {
				required = append(required, p.name)
			}
		}
		var d ToolDef
		d.Type = "function"
		d.Function.Name = t.name
		d.Function.Description = t.desc
		d.Function.Parameters = map[string]any{"type": "object", "properties": props, "required": required}
		if required == nil {
			d.Function.Parameters["required"] = []string{}
		}
		out = append(out, d)
	}
	return out
}

// Translate validates a typed call and returns the MCP tool and its CLI
// arguments. Anything missing, out of range or outside the menu is an
// error — the call is never executed.
func Translate(name string, raw json.RawMessage, services []string) (string, map[string]string, error) {
	var t *typedTool
	for i := range typedTools {
		if typedTools[i].name == name {
			t = &typedTools[i]
		}
	}
	if t == nil {
		return "", nil, fmt.Errorf("unbekanntes Werkzeug %q: nicht im Menü (approve, apply, converge und Co. gibt es hier nicht)", name)
	}
	var a map[string]any
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "" {
		a = map[string]any{}
	} else if err := json.Unmarshal(raw, &a); err != nil || a == nil {
		return "", nil, fmt.Errorf("%s: Argumente sind kein JSON-Objekt", name)
	}
	for _, p := range t.params {
		v, present := a[p.name]
		if !present || v == nil || (p.typ == "string" && str(a, p.name) == "") {
			if p.required {
				return "", nil, fmt.Errorf("%s: Parameter %s fehlt", name, p.name)
			}
			continue
		}
		switch p.typ {
		case "string":
			s, ok := v.(string)
			if !ok {
				return "", nil, fmt.Errorf("%s: %s muss ein String sein", name, p.name)
			}
			if len(p.enum) > 0 && !slices.Contains(p.enum, s) {
				return "", nil, fmt.Errorf("%s: %s muss eines von %s sein, nicht %q", name, p.name, strings.Join(p.enum, ", "), s)
			}
			if p.name == "service" {
				if !serviceName.MatchString(s) {
					return "", nil, fmt.Errorf("%s: service %q ist kein Dienstname", name, s)
				}
				if len(services) > 0 && !slices.Contains(services, s) {
					return "", nil, fmt.Errorf("%s: Dienst %q gibt es nicht; installiert: %s", name, s, strings.Join(services, ", "))
				}
			}
			if p.name == "key" && !valueKey.MatchString(s) {
				return "", nil, fmt.Errorf("%s: key %q ist kein values.yml-Schlüssel (GROSSBUCHSTABEN, kein Pfad)", name, s)
			}
			if p.name == "value" && strings.ContainsAny(s, "\n\r") {
				return "", nil, fmt.Errorf("%s: value darf keine Zeilenumbrüche enthalten", name)
			}
		case "integer":
			f, ok := v.(float64)
			if !ok || f != float64(int(f)) {
				return "", nil, fmt.Errorf("%s: %s muss eine ganze Zahl sein", name, p.name)
			}
			max := map[string]float64{"minutes": 1440, "hours": 168}[p.name]
			if f < 1 || f > max {
				return "", nil, fmt.Errorf("%s: %s muss zwischen 1 und %d liegen", name, p.name, int(max))
			}
		}
	}
	return t.mcp, map[string]string{"args": t.cli(a)}, nil
}
