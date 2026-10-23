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
	"repair":   {{name: "probe", typ: "string", desc: "die Probe des Befunds aus laib diag, z. B. unit", required: true}, optSvc},
	"logs":     {svcParam, sinceParam},
	"inspect":  {svcParam},
	"read":     {svcParam, {name: "path", typ: "string", desc: "die Datei in den Datenpfaden des Dienstes", required: true}},
	"state":    {{name: "path", typ: "string", desc: "die Datei im State-Repo, z. B. policy.yml oder services/jellyfin/values.yml; leer = die Liste der Dateien"}},
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
	"context": {sinceParam, {name: "full", typ: "boolean", flag: "--full", desc: "das ganze Bündel: alle Dienste, das Journal ungekürzt (sonst der erste Blick, #579)"}},
	"inbox": {{name: "id", typ: "string", desc: "ein Vorschlag ganz, mit Karte, auch ein entschiedener"}, {name: "all", typ: "boolean", flag: "--all", desc: "auch die entschiedenen, neueste zuerst, ohne Karte"},
		{name: "limit", typ: "string", flag: "--limit", desc: "höchstens so viele entschiedene (20; 0 = alle)"}},
	// propose and wish build their argv in code (mcpArgv)
	"propose": {{name: "reason", typ: "string", desc: "ein Satz: warum", required: true},
		{name: "service", typ: "string", desc: "der Dienst, dessen values.yml sich ändert"},
		{name: "key", typ: "string", desc: "der Schlüssel in values.yml, z. B. IMAGE"},
		{name: "value", typ: "string", desc: "der neue Wert, eine Zeile; mit path ein YAML-Wert, leer entfernt ihn"},
		{name: "path", typ: "string", desc: "statt service/key: der Pfad, an dem value steht, Datei:a.b.c, z. B. box.yml:people.mama.signal (#598)"},
		{name: "finding", typ: "string", desc: "der Befund aus laib diag, den der Vorschlag beheben soll: probe und Dienst, z. B. unit jellyfin; laib misst danach, ob er weg ist"},
		{name: "evidence", typ: "string", desc: "was du gesehen hast: die Zeile aus diag oder dem Log"},
		{name: "undo", typ: "string", desc: "wie man es rückgängig macht, ein Satz"},
		{name: "sources", typ: "array", desc: "die Adressen aus dem Netz, auf die sich der Vorschlag stützt; dann gibt ihn nur ein Mensch frei"},
		{name: "hand", typ: "string", desc: "mit verb: die Hand in policy.yml, deren Verbliste ein Verb dazubekommt (#575); ein Mensch entscheidet"},
		{name: "verb", typ: "string", desc: "mit hand: das Verb, z. B. scan"}},
	"wish": {{name: "text", typ: "string", desc: "der Vorschlag, ein bis zwei Sätze", required: true},
		{name: "saw", typ: "string", desc: "was du gesehen hast, mit Zahlen"},
		{name: "better", typ: "string", enum: []string{"wartung", "familie", "beides"}, desc: "was besser wird: die Wartung, der Komfort der Familie oder beides"}},
	"apply":   {{name: "proposal", typ: "string", flag: "--proposal", desc: "die Nummer deines eigenen offenen Vorschlags; Laib sichert vorher, prüft danach und nimmt bei Rot zurück", required: true}},
	"backup":  {{name: "action", typ: "string", enum: []string{"check", "run"}, desc: "check: restic prüft eine Stichprobe und stellt einen Pfad probeweise wieder her und vergleicht; run: eine Sicherung jetzt", required: true}},
	"updates": {{name: "now", typ: "boolean", flag: "--now", desc: "den nächtlichen Lauf jetzt starten (Stufe apply): erst Laib selbst, dann die Images; das Ergebnis zeigt laib_updates danach"}},
	"net": {{name: "view", typ: "string", enum: []string{"geraete"}, desc: "leer: Namen, Ports und Wege aus der Konfiguration; geraete: die Geräte im Heimnetz, die die Box gerade sieht (Adresse, MAC, Name, Hersteller)"},
		{name: "wecken", typ: "boolean", flag: "--wecken", desc: "geraete: vorher einmal bei jeder Adresse im /24 anklopfen (ein UDP-Paket, kein Portscan), dauert etwa 3 s"}},
	"person_invite": {{name: "user", typ: "string", desc: "der Benutzername in LLDAP, klein, ohne Leerzeichen", required: true},
		{name: "first", typ: "string", flag: "--first", desc: "Vorname", required: true}, {name: "last", typ: "string", flag: "--last", desc: "Nachname"},
		{name: "email", typ: "string", flag: "--email", desc: "wohin der Link zum Passwort geht", required: true},
		{name: "group", typ: "string", flag: "--group", enum: []string{"family", "admins"}, desc: "neu: family, wenn leer; beim erneuten Senden bleibt die Gruppe"},
		{name: "resend", typ: "boolean", flag: "--resend", desc: "den Link für einen eingeladenen Benutzer noch einmal schicken"},
		{name: "signal", typ: "string", flag: "--signal", desc: "die Signal-Nummer, +49…: wird ein Vorschlag für box.yml people, den ein Mensch freigibt"}},
	"person_signal": {{name: "user", typ: "string", desc: "der Name in box.yml people, klein (wie der Benutzername)", required: true},
		{name: "number", typ: "string", desc: "die Signal-Nummer mit Ländervorwahl, +49…", required: true}},
	"person_pending": {},
	"calendar": {{name: "action", typ: "string", enum: []string{"subscribe", "unsubscribe", "list"}, required: true}, {name: "person", typ: "string", desc: "wem der Kalender gehört (LLDAP-Benutzer, haus für die Familie)"},
		{name: "name", typ: "string", desc: "kurz, klein, a-z 0-9 -"}, {name: "kind", typ: "string", enum: []string{"google", "abo"}, desc: "google: Googles geheime iCal-Adresse; abo: ein öffentlicher Kalender"},
		{name: "title", typ: "string", desc: "der Name, den die Familie sieht"}},
	"zuhause": {{name: "action", typ: "string", enum: []string{"show", "set"}, required: true},
		{name: "street", typ: "string", flag: "--street", desc: "Straße und Hausnummer"}, {name: "postcode", typ: "string", flag: "--postcode", desc: "Postleitzahl"},
		{name: "city", typ: "string", flag: "--city", desc: "Ort"}, {name: "country", typ: "string", flag: "--country", desc: "Land, zwei Buchstaben (DE)"},
		{name: "latitude", typ: "string", flag: "--latitude", desc: "Breitengrad, z. B. 52.1234; mit longitude"}, {name: "longitude", typ: "string", flag: "--longitude", desc: "Längengrad, z. B. 8.0456"},
		{name: "elevation", typ: "string", flag: "--elevation", desc: "Höhe in Metern, ganze Zahl"}},
	"scan": {{name: "action", typ: "string", enum: []string{"status", "start", "weiter", "abschliessen", "verwerfen", "redo", "suchen", "pruefen", "probe", "einrichten"}, required: true,
		desc: "status: was der Scanner gerade tut; start: einen Stapel scannen; nach einem Abbruch weiter, abschliessen oder verwerfen; redo: einen Stapel aus dem Archiv neu trennen; suchen: Scanner im Netz suchen; pruefen: den Scanner ohne Papier prüfen; probe: eine Probeseite, nicht nach Paperless; einrichten: ein Vorschlag für den Scanner"},
		{name: "geraet", typ: "string", flag: "--geraet", desc: "einrichten: das SANE-Gerät aus suche.funde oder von Hand, epson2:net:192.168.1.5"},
		{name: "person", typ: "string", flag: "--person", desc: "wem die Abschlussmeldung gilt (box.yml people); leer: notify.person"},
		{name: "stapel", typ: "string", flag: "--stapel", desc: "redo: der Stapel, JJJJ-MM-TTThh-mm-ss"}},
	"skill": {{name: "action", typ: "string", enum: []string{"list", "enable", "pause"}, required: true}, {name: "name", typ: "string", desc: "die Fähigkeit"}},
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
	if verb == "calendar" {
		props["stdin"] = map[string]any{"type": "string", "description": "subscribe: die Kalender-Adresse (https:// oder webcal://); sie kommt in keiner Antwort vor"}
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
		if val["path"] != "" {
			argv = append(argv, "--path", val["path"]+"="+val["value"])
		} else if val["service"] != "" || val["key"] != "" || val["value"] != "" {
			if val["service"] == "" || val["key"] == "" {
				return nil, "", fmt.Errorf("service, key und value gehören zusammen")
			}
			argv = append(argv, "--set", "services/"+val["service"]+"/values.yml:"+val["key"]+"="+val["value"])
		}
		if (val["hand"] == "") != (val["verb"] == "") {
			return nil, "", fmt.Errorf("hand und verb gehören zusammen")
		} else if val["hand"] != "" {
			argv = append(argv, "--grant", val["hand"], val["verb"])
		}
		for _, k := range []string{"finding", "evidence", "undo"} {
			if val[k] != "" {
				argv = append(argv, "--"+k, val[k])
			}
		}
		for _, src := range rest {
			argv = append(argv, "--source", src)
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
