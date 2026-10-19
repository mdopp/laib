package mcp

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/mdopp/laib/internal/state"
)

// Typed MCP arguments (#290): each verb an agent uses most publishes its
// parameters as JSON schema, and the call's arguments become argv elements
// directly — no command line joined by the agent and split again here.
// `args` (one command line) stays accepted for every verb.

type mcpParam struct {
	name, typ, desc string // typ: string | array | boolean (a flag, set when true)
	flag            string // "" = positional, "--" = the rest of argv
	required        bool
	enum            []string
}

var svcParam = mcpParam{name: "service", typ: "string", desc: "der Dienst", required: true}
var optSvc = mcpParam{name: "service", typ: "string", desc: "nur dieser Dienst; leer = die ganze Box"}
var sinceParam = mcpParam{name: "since", typ: "string", flag: "--since", desc: "wie weit zurück, z. B. 60m, 24h"}
var cmdParams = []mcpParam{svcParam, {name: "container", typ: "string", flag: "--container", desc: "einer der Container des Dienstes; leer = der erste"}, {name: "command", typ: "array", flag: "--", desc: "das Programm und seine Argumente, je ein Element", required: true}}

var mcpParams = map[string][]mcpParam{
	"status":   {optSvc},
	"diag":     {optSvc},
	"logs":     {svcParam, sinceParam},
	"inspect":  {svcParam},
	"read":     {svcParam, {name: "path", typ: "string", desc: "die Datei in den Datenpfaden des Dienstes", required: true}},
	"write":    {svcParam, {name: "path", typ: "string", desc: "die Datei in den Datenpfaden des Dienstes", required: true}},
	"look":     cmdParams,
	"exec":     cmdParams,
	"restart":  {svcParam},
	"start":    {svcParam},
	"stop":     {svcParam, {name: "until", typ: "string", flag: "--until", desc: "wie lange er aus bleibt, höchstens ein Tag, z. B. 2h"}, {name: "reason", typ: "string", desc: "warum, ein Satz"}},
	"backups":  {},
	"escalate": {svcParam, {name: "summary", typ: "string", desc: "was du weißt, ein bis drei Sätze", required: true}},
	"note": {{name: "kind", typ: "string", flag: "--kind", enum: []string{"watch", "incident", "report"}, required: true, desc: "incident nach Reparatur oder Eskalation, watch wenn alles in Ordnung war, report für den Morgenbericht"},
		{name: "text", typ: "string", desc: "deutsch, drei bis fünf Sätze, Fakten mit Uhrzeit, rot zuerst", required: true}},
	"journal": {sinceParam},
	"context": {sinceParam},
	// propose and wish build their argv in code (mcpArgv)
	"propose": {{name: "reason", typ: "string", desc: "ein Satz: warum", required: true},
		{name: "service", typ: "string", desc: "der Dienst, dessen values.yml sich ändert"},
		{name: "key", typ: "string", desc: "der Schlüssel in values.yml, z. B. IMAGE"},
		{name: "value", typ: "string", desc: "der neue Wert, eine Zeile"}},
	"wish": {{name: "text", typ: "string", desc: "der Vorschlag, ein bis zwei Sätze", required: true},
		{name: "saw", typ: "string", desc: "was du gesehen hast, mit Zahlen"},
		{name: "better", typ: "string", enum: []string{"wartung", "familie", "beides"}, desc: "was besser wird: die Wartung, der Komfort der Familie oder beides"}},
	"apply":   {{name: "proposal", typ: "string", flag: "--proposal", desc: "die Nummer deines eigenen offenen Vorschlags; Laib sichert vorher, prüft danach und nimmt bei Rot zurück", required: true}},
	"backup":  {{name: "action", typ: "string", enum: []string{"check", "run"}, desc: "check: restic prüft eine Stichprobe und stellt einen Pfad probeweise wieder her und vergleicht; run: eine Sicherung jetzt", required: true}},
	"updates": {},
	"net":     {},
	"person_invite": {{name: "user", typ: "string", desc: "der Benutzername in LLDAP, klein, ohne Leerzeichen", required: true},
		{name: "first", typ: "string", flag: "--first", desc: "Vorname", required: true}, {name: "last", typ: "string", flag: "--last", desc: "Nachname"},
		{name: "email", typ: "string", flag: "--email", desc: "wohin der Link zum Passwort geht", required: true},
		{name: "group", typ: "string", flag: "--group", enum: []string{"family", "admins"}, desc: "neu: family, wenn leer; beim erneuten Senden bleibt die Gruppe"},
		{name: "resend", typ: "boolean", flag: "--resend", desc: "den Link für einen eingeladenen Benutzer noch einmal schicken"}},
	"person_pending": {},
	"skill":          {{name: "action", typ: "string", enum: []string{"list", "enable", "pause"}, required: true}, {name: "name", typ: "string", desc: "die Fähigkeit"}},
}

var (
	valueKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,60}$`)
	betterText = map[string]string{"wartung": "die Wartung", "familie": "der Komfort der Familie", "beides": "die Wartung und der Komfort der Familie"}
)

// mcpSchema is a verb's input schema: its typed parameters, the installed
// services as the service enum; a verb without parameters takes `args`.
func mcpSchema(st *state.State, verb string) map[string]any {
	params, ok := mcpParams[verb]
	if !ok {
		return InputSchema(verb)
	}
	props := map[string]any{}
	required := []string{}
	for _, p := range params {
		prop := map[string]any{"type": p.typ, "description": p.desc}
		if p.typ == "array" {
			prop["items"] = map[string]any{"type": "string"}
		}
		if p.name == "service" && len(st.Services) > 0 {
			prop["enum"] = slices.Sorted(maps.Keys(st.Services))
		}
		if len(p.enum) > 0 {
			prop["enum"] = p.enum
		}
		props[p.name] = prop
		if p.required {
			required = append(required, p.name)
		}
	}
	if verb == "propose" {
		props["patch"] = map[string]any{"type": "string", "description": "statt service/key/value: ein unified diff gegen das Zustands-Repo"}
	}
	if verb == "write" {
		props["stdin"] = map[string]any{"type": "string", "description": "der Inhalt der Datei"}
	}
	return map[string]any{"type": "object", "properties": props, "required": required}
}

// Argv turns a call's arguments into the verb's argv and stdin. Typed
// arguments are checked against the schema first; anything outside it is
// an error and the verb never runs.
func Argv(st *state.State, verb string, a map[string]any) ([]string, string, error) {
	stdin, _ := a["stdin"].(string)
	if p, _ := a["patch"].(string); p != "" {
		stdin = p
	}
	params, typed := mcpParams[verb]
	raw, hasRaw := a["args"]
	if !typed || hasRaw {
		s, ok := raw.(string)
		if hasRaw && !ok {
			return nil, "", fmt.Errorf("args muss ein String sein")
		}
		argv := SplitArgs(s)
		if a["patch"] != nil {
			argv = append([]string{"--patch"}, argv...)
		}
		return argv, stdin, nil
	}
	val := map[string]string{}
	var rest []string
	for k, v := range a {
		if k == "patch" || k == "stdin" {
			continue
		}
		i := slices.IndexFunc(params, func(p mcpParam) bool { return p.name == k })
		if i < 0 {
			return nil, "", fmt.Errorf("unbekannter Parameter %q (erlaubt: %s)", k, names(params))
		}
		p := params[i]
		if p.typ == "array" {
			items, ok := v.([]any)
			for _, it := range items {
				s, isStr := it.(string)
				ok = ok && isStr
				rest = append(rest, s)
			}
			if !ok || len(items) == 0 {
				return nil, "", fmt.Errorf("%s muss eine Liste von Strings sein", k)
			}
			continue
		}
		if p.typ == "boolean" {
			b, ok := v.(bool)
			if !ok {
				return nil, "", fmt.Errorf("%s muss true oder false sein", k)
			}
			if b {
				val[k] = "true"
			}
			continue
		}
		s, ok := v.(string)
		if !ok {
			return nil, "", fmt.Errorf("%s muss ein String sein", k)
		}
		switch {
		case p.name == "service" && s != "" && st.Services[s] == nil:
			return nil, "", fmt.Errorf("Dienst %q gibt es nicht; installiert: %s", s, strings.Join(slices.Sorted(maps.Keys(st.Services)), ", "))
		case len(p.enum) > 0 && !slices.Contains(p.enum, s):
			return nil, "", fmt.Errorf("%s muss eines von %s sein, nicht %q", k, strings.Join(p.enum, ", "), s)
		case k == "key" && !valueKeyRe.MatchString(s):
			return nil, "", fmt.Errorf("key %q ist kein values.yml-Schlüssel (GROSSBUCHSTABEN, kein Pfad)", s)
		case k == "value" && strings.ContainsAny(s, "\n\r"):
			return nil, "", fmt.Errorf("value darf keine Zeilenumbrüche enthalten")
		}
		val[k] = s
	}
	for _, p := range params {
		if p.required && val[p.name] == "" && (p.typ != "array" || len(rest) == 0) {
			return nil, "", fmt.Errorf("Parameter %s fehlt", p.name)
		}
	}
	switch verb {
	case "propose":
		argv := []string{val["reason"]}
		if val["service"] != "" || val["key"] != "" || val["value"] != "" {
			if val["service"] == "" || val["key"] == "" {
				return nil, "", fmt.Errorf("service, key und value gehören zusammen")
			}
			argv = append(argv, "--set", "services/"+val["service"]+"/values.yml:"+val["key"]+"="+val["value"])
		}
		if a["patch"] != nil {
			argv = append([]string{"--patch"}, argv...)
		}
		return argv, stdin, nil
	case "backup":
		if val["action"] == "check" {
			return []string{"check"}, stdin, nil
		}
		return nil, stdin, nil
	case "wish":
		text := val["text"]
		if val["saw"] != "" {
			text += "\n\nGesehen: " + val["saw"]
		}
		if val["better"] != "" {
			text += "\nBesser wird: " + betterText[val["better"]]
		}
		return []string{text}, stdin, nil
	}
	var argv []string
	for _, p := range params {
		switch {
		case p.flag == "--" && len(rest) > 0:
			argv = append(argv, append([]string{"--"}, rest...)...)
		case val[p.name] == "":
		case p.typ == "boolean":
			argv = append(argv, p.flag)
		case p.flag != "":
			argv = append(argv, p.flag, val[p.name])
		default:
			argv = append(argv, val[p.name])
		}
	}
	return argv, stdin, nil
}

func names(ps []mcpParam) string {
	var out []string
	for _, p := range ps {
		out = append(out, p.name)
	}
	return strings.Join(out, ", ")
}
