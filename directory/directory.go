// Package directory is the Bote's directory (ADR 0030, ADR 0021): it knows
// where each person stands — the directory of the Bote's tools, its levels and
// context — and answers help in plain words, in the assistant's name (box.yml
// assistant, „Homes“). It reads nothing from the box and changes nothing; the
// model only passes text through, where a person is is this code.
package directory

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/mdopp/laib/mcpserver"
)

const idle = 30 * time.Minute

type session struct {
	ctx   string // "" = the directory, or one of the Bote's tools (#122)
	lang  string // de or en
	last  time.Time
	admin bool // the admin's number: the box's level is offered (#863)
}

// Who is the assistant and who the admin is.
type Who struct {
	Name    string                    // the assistant's, box.yml assistant (ADR 0030)
	Resolve func(label string) string // the session's label (a profile name) → its number; "" when unknown or ambiguous
	Admins  []string                  // the admin's numbers (the Bote's ADMIN_NUMBER): only they are offered the box (#863)
}

// Admin: the session is the admin's number, or a label that resolves to it.
// Anyone else, an empty or ambiguous label too, is family.
func (c Who) Admin(sid string) bool {
	if sid == "" {
		return false
	}
	if slices.Contains(c.Admins, sid) {
		return true
	}
	return c.Resolve != nil && slices.Contains(c.Admins, c.Resolve(sid)) && c.Resolve(sid) != ""
}

// SignalContacts resolves a sender's label to their number through
// signal-cli's contacts (#127): Hermes shows the model the profile name, not
// the number. Exactly one contact must match, else the person is family: a
// look-alike name never gets the admin's box.
func SignalContacts(base, account string, h *http.Client) func(string) string {
	if h == nil {
		h = &http.Client{Timeout: 5 * time.Second}
	}
	var mu sync.Mutex
	var cached map[string][]string
	var at time.Time
	return func(label string) string {
		mu.Lock()
		defer mu.Unlock()
		if cached == nil || time.Since(at) > 5*time.Minute {
			cached, at = map[string][]string{}, time.Now()
			resp, err := h.Get(base + "/v1/contacts/" + url.PathEscape(account))
			if err == nil {
				var list []struct {
					Number, Uuid, Name string
					ProfileName        string `json:"profile_name"`
					GivenName          string `json:"given_name"`
					Profile            struct {
						GivenName string `json:"given_name"`
						Lastname  string `json:"lastname"`
					}
					Nickname struct{ Name string }
				}
				json.NewDecoder(resp.Body).Decode(&list)
				resp.Body.Close()
				for _, c := range list {
					for _, k := range []string{c.Number, c.Uuid, c.Name, c.ProfileName, c.GivenName, c.Nickname.Name, c.Profile.GivenName,
						strings.TrimSpace(c.Profile.GivenName + " " + c.Profile.Lastname)} {
						if k = strings.ToLower(strings.TrimSpace(k)); k != "" && !slices.Contains(cached[k], c.Number) {
							cached[k] = append(cached[k], c.Number)
						}
					}
				}
			}
		}
		if n := cached[strings.ToLower(strings.TrimSpace(label))]; len(n) == 1 {
			return n[0]
		}
		return ""
	}
}

// Terminal holds one session per person (the Bote passes who is writing).
type Terminal struct {
	mu       sync.Mutex
	sessions map[string]*session
	now      func() time.Time
	caps     func() []string
	who      Who
}

// New takes the Bote's tools, read on every greeting (post-deploy rewrites
// them), and who speaks.
func New(caps func() []string, who Who) *Terminal {
	if who.Name == "" {
		who.Name = "Homes"
	}
	return &Terminal{sessions: map[string]*session{}, now: time.Now, caps: caps, who: who}
}

// words is the directory and the navigation in the person's language (#122).
type words struct {
	dir, enter, connect, bye string
	labels                   map[string]string
	levels                   map[string]string // the level's word in the prompt, never the tool's name (ADR 0021)
}

var lang = map[string]words{
	"de": {"VERZEICHNIS", "NUMMER WÄHLEN, ODER EINFACH FRAGEN.", "VERBINDE MIT %s ...", "AUF WIEDERSEHEN.",
		map[string]string{"laib": "BOX-STATUS UND VORSCHLÄGE", "frage": "WEBSUCHE MIT QUELLEN",
			"ha": "LICHT, SCHALTER, MUSIK", "automat": "AUTOMATIONEN", "haushalt": "KALENDER, KONTAKTE, NOTIZEN"},
		map[string]string{"laib": "BOX", "frage": "FRAGE", "ha": "GERÄTE", "automat": "AUTOMATIK", "haushalt": "HAUSHALT"}},
	"en": {"DIRECTORY", "ENTER SELECTION, OR JUST ASK.", "CONNECTING TO %s ...", "GOODBYE.",
		map[string]string{"laib": "BOX STATUS AND PROPOSALS", "frage": "WEB SEARCH WITH SOURCES",
			"ha": "LIGHTS, SWITCHES, MUSIC", "automat": "AUTOMATIONS", "haushalt": "CALENDAR, CONTACTS, NOTES"},
		map[string]string{"laib": "BOX", "frage": "SEARCH", "ha": "DEVICES", "automat": "AUTOMATIONS", "haushalt": "HOUSEHOLD"}},
}

// named: the assistant's name, as a word of its own, is a hello.
func (t *Terminal) named(in string) bool {
	for _, f := range strings.Fields(in) {
		if strings.Trim(f, ",.!?:;") == strings.ToUpper(t.who.Name) {
			return true
		}
	}
	return false
}

// aufgabeRe is Homes's „Gemeinsam mit Homes“ (#878): the conversation
// about one Box-Aufgabe, which the model reads with laib_aufgabe.
var aufgabeRe = regexp.MustCompile(`^AUFGABE #?([0-9]{1,6}) MIT DIR ERLEDIGEN$`)

// Talk is one message: a screen to show as is (its last line is the prompt),
// or "WEITER: <tool>\nPROMPT: <prompt>" when it is a request for the Bote in
// the current context ("-" at the top: any tool); "AUFGABE: <n>" follows
// when the person starts the conversation about Box-Aufgabe n (#878).
func (t *Terminal) Talk(sid, language, line string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	for k, s := range t.sessions {
		if now.Sub(s.last) > idle {
			delete(t.sessions, k)
		}
	}
	if sid == "" {
		sid = "default"
	}
	in := strings.ToUpper(strings.TrimSpace(line))
	in = strings.TrimRight(in, ".!?")
	s, ok := t.sessions[sid]
	if !ok {
		s = &session{lang: "en", admin: t.who.Admin(sid)}
		t.sessions[sid] = s
	}
	if _, known := lang[language]; known {
		s.lang = language
	}
	s.last = now
	w := lang[s.lang]
	if said(in, 3, "LOGOFF", "LOG OFF", "BYE", "EXIT", "QUIT", "TSCHÜSS", "ENDE") {
		delete(t.sessions, sid)
		return w.bye
	}
	if m := aufgabeRe.FindStringSubmatch(in); m != nil { // #878: a Box-Aufgabe with the Bote, from Homes
		s.ctx = ""
		return "WEITER: -\nPROMPT: " + t.prompt(s) + "\nAUFGABE: " + m[1]
	}
	if (said(in, 3) && t.named(in)) || said(in, 2, "HALLO", "HELLO") || said(in, 3, "HILFE", "HELP") || said(in, 5, "WAS KANNST DU", "WHAT CAN YOU DO") {
		s.ctx = ""
		return t.help(s)
	}
	if said(in, 3, "MENÜ", "MENU", "VERZEICHNIS", "DIRECTORY") {
		s.ctx = ""
		return t.directory(s, w)
	}
	if len(in) < 16 && has(in, "ZURÜCK", "ZURUECK", "BACK") {
		s.ctx = ""
		return t.directory(s, w)
	}
	tools := t.tools(s)
	if s.ctx == "" {
		n := -1
		if v, err := strconv.Atoi(in); err == nil {
			n = v
		}
		for i, k := range tools {
			if in == strings.ToUpper(k) {
				n = i + 1
			}
		}
		switch {
		case n >= 1 && n <= len(tools):
			s.ctx = tools[n-1]
			return fmt.Sprintf(w.connect, word(w.labels, s.ctx)) + "\n\n" + t.prompt(s)
		case n >= 0:
			return t.directory(s, w)
		default:
			return "WEITER: -\nPROMPT: " + t.prompt(s)
		}
	}
	return "WEITER: " + s.ctx + "\nPROMPT: " + t.prompt(s)
}

// prompt is where the person is, in the assistant's name: HOMES>,
// HOMES/GERÄTE>.
func (t *Terminal) prompt(s *session) string {
	name := strings.ToUpper(t.who.Name)
	if s.ctx == "" {
		return name + ">"
	}
	return name + "/" + word(lang[s.lang].levels, s.ctx) + ">"
}

// word is a tool's word from labels or levels, its name in capitals when
// the language has none.
func word(m map[string]string, k string) string {
	if l, ok := m[k]; ok {
		return l
	}
	return strings.ToUpper(k)
}

// tools are the Bote's tools this person is offered: the box (laib) only
// to the admin (#863).
func (t *Terminal) tools(s *session) []string {
	if t.caps == nil {
		return nil
	}
	return slices.DeleteFunc(t.caps(), func(k string) bool { return k == "laib" && !s.admin })
}

// examples: a sentence per tool, in the order help offers them, then ones
// the model answers alone, so there are always five (#863).
var examples = map[string][][2]string{
	"de": {{"ha", "Mach das Licht in der Küche aus."}, {"haushalt", "Erinnere mich morgen um acht an den Müll."},
		{"frage", "Wie hoch ist die Zugspitze?"}, {"automat", "Sag mir Bescheid, wenn die Waschmaschine fertig ist."},
		{"laib", "Wie geht es der Box?"},
		{"", "Schreib mir eine Einkaufsliste für Pfannkuchen."}, {"", "Wie sagt man ‚Guten Morgen‘ auf Englisch?"},
		{"", "Erklär mir in zwei Sätzen, wie ein Regenbogen entsteht."}, {"", "Hilf mir, eine kurze Geburtstagskarte zu schreiben."},
		{"", "Was koche ich heute mit Kartoffeln und Lauch?"}},
	"en": {{"ha", "Turn off the kitchen light."}, {"haushalt", "Remind me tomorrow at eight to take out the bin."},
		{"frage", "How high is the Zugspitze?"}, {"automat", "Tell me when the washing machine is done."},
		{"laib", "How is the box doing?"},
		{"", "Write me a shopping list for pancakes."}, {"", "How do you say ‘good morning’ in German?"},
		{"", "Explain in two sentences how a rainbow forms."}, {"", "Help me write a short birthday card."},
		{"", "What can I cook tonight with potatoes and leeks?"}},
}

// help is plain words for the person, no terminal (#863): who speaks, and
// five sentences to send as they are. "TEXT:" tells the Bote's plugin to
// pass it on unchanged and without a code block.
func (t *Terminal) help(s *session) string {
	intro, open, close := "Hallo, hier ist %s. Schreib mir einfach, was du brauchst, so wie du es einem Menschen sagen würdest. Zum Beispiel:", "„", "“"
	if s.lang == "en" {
		intro, open, close = "Hello, this is %s. Just write what you need, the way you would tell a person. For example:", "“", "”"
	}
	have := t.tools(s)
	var b strings.Builder
	fmt.Fprintf(&b, intro+"\n", t.who.Name)
	n := 0
	for _, e := range examples[s.lang] {
		if n < 5 && (e[0] == "" || slices.Contains(have, e[0])) {
			b.WriteString("\n- " + open + e[1] + close)
			n++
		}
	}
	return "TEXT:\n" + b.String()
}

func (t *Terminal) directory(s *session, w words) string {
	var b strings.Builder
	b.WriteString(w.dir + "\n" + strings.Repeat("-", len([]rune(w.dir))) + "\n")
	tools := t.tools(s)
	for i, k := range tools {
		fmt.Fprintf(&b, "  %d  %-28s (%s)\n", i+1, word(w.labels, k), word(w.levels, k))
	}
	b.WriteString("\n" + w.enter + "\n\n")
	return b.String() + t.prompt(s)
}

// said: a message of at most max words that holds one of the phrases as words of
// their own (none given: any). A longer message is a request for the Bote,
// whatever word it holds (#587: a quote's ABSENDER held ENDE, the Bote said goodbye).
func said(in string, max int, phrases ...string) bool {
	f := strings.FieldsFunc(in, func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune(",;:!?.\"[]()", r) })
	if len(f) > max {
		return false
	}
	padded := " " + strings.Join(f, " ") + " "
	for _, p := range phrases {
		if strings.Contains(padded, " "+p+" ") {
			return true
		}
	}
	return len(phrases) == 0
}

func has(in string, words ...string) bool {
	for _, w := range words {
		if strings.Contains(in, w) {
			return true
		}
	}
	return false
}

// Server is the MCP tool the Bote calls with every message (#122): it knows
// where the person is and answers with a screen or with WEITER.
func Server(t *Terminal, version string) *mcpserver.Server {
	return &mcpserver.Server{
		Name:         "directory",
		Version:      version,
		Instructions: "Das Verzeichnis des Boten: Werkzeuge, Ebenen und Kontext. Jede Nachricht zuerst hierher.",
		Tools: []mcpserver.Tool{{
			Name: "directory",
			Description: "Die Oberfläche des Boten: ein Verzeichnis deiner Werkzeuge mit Ebenen. Ruf es mit JEDER Nachricht zuerst auf. " +
				"Kommt ein Bildschirm zurück, zeig ihn unverändert in einem Codeblock. " +
				"Kommt \"WEITER: <werkzeug>\" und \"PROMPT: <prompt>\" zurück, beantworte die Nachricht selbst, " +
				"im Kontext nur mit diesem Werkzeug (bei \"-\" mit jedem), und beginne die Antwort mit der Prompt-Zeile. " +
				"Beginnt die Antwort mit \"TEXT:\", gib den Text danach unverändert weiter, ohne Codeblock. " +
				"Kommt \"AUFGABE: <n>\" mit, lies Box-Aufgabe n mit laib_aufgabe (zeigen) und führe die Person hindurch.",
			Schema: map[string]any{"type": "object", "required": []string{"eingabe"}, "properties": map[string]any{
				"eingabe": map[string]any{"type": "string", "description": "die Nachricht der Person, unverändert"},
				"sitzung": map[string]any{"type": "string", "description": "wer schreibt, so wie du die Person siehst (Name oder Nummer); dieselbe Person, dieselbe Sitzung"},
				"sprache": map[string]any{"type": "string", "enum": []string{"de", "en"}, "description": "die Sprache der Person; ohne Angabe de"},
			}},
			Call: func(_ context.Context, args map[string]any) (string, error) {
				l := mcpserver.Str(args, "sprache")
				if l == "" {
					l = "de"
				}
				return t.Talk(mcpserver.Str(args, "sitzung"), l, mcpserver.Str(args, "eingabe")), nil
			},
		}},
	}
}
