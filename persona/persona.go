// Package persona is the Bote's persona engine (ADR 0020, ADR 0021): it knows
// where each person stands — the directory of the Bote's tools, its levels and
// context — and speaks as the persona of that person (the house's, or the one
// box.yml chatPersonas gives their number). The persona brings its name, its
// greeting and its gimmicks: Joshua's are WOPR's directory, tic-tac-toe (it
// never loses) and Global Thermonuclear War (nobody wins). It reads nothing
// from the box and changes nothing; the model only passes text through, where
// a person is is this code.
package persona

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/mdopp/laib/mcpserver"
	"gopkg.in/yaml.v3"
)

const idle = 30 * time.Minute

type mode int

const (
	modeMenu mode = iota
	modePlayers
	modeTicTacToe
	modeChess
	modeSide
	modeTargets
)

type session struct {
	mode  mode
	board [9]byte
	side  string
	ctx   string // "" = the directory, games, or one of the Bote's tools (#122)
	lang  string // de or en
	last  time.Time
	p     *Persona // who speaks to this person
	admin bool     // the admin's number: the box's level is offered (#863)
}

// Cast is who speaks to whom: the house persona, and per Signal number another
// one (box.yml chatPersonas, rendered to render/persona/chat.yml).
type Cast struct {
	House   *Persona
	ByName  map[string]*Persona
	Chat    map[string]string         // number → persona name
	Resolve func(label string) string // the session's label (a profile name) → its number; "" when unknown or ambiguous
	Admins  []string                  // the admin's numbers (the Bote's ADMIN_NUMBER): only they are offered the box (#863)
}

// Admin: the session is the admin's number, or a label that resolves to it.
// Anyone else, an empty or ambiguous label too, is family.
func (c Cast) Admin(sid string) bool {
	if sid == "" {
		return false
	}
	if slices.Contains(c.Admins, sid) {
		return true
	}
	return c.Resolve != nil && slices.Contains(c.Admins, c.Resolve(sid)) && c.Resolve(sid) != ""
}

// For is the persona of one session: its id is the number, or the name
// Hermes shows the model, resolved to a number.
func (c Cast) For(sid string) *Persona {
	if p := c.ByName[c.Chat[sid]]; p != nil {
		return p
	}
	if c.Resolve != nil {
		if p := c.ByName[c.Chat[c.Resolve(sid)]]; p != nil {
			return p
		}
	}
	return c.House
}

// SignalContacts resolves a sender's label to their number through
// signal-cli's contacts (#127): Hermes shows the model the profile name, not
// the number. Exactly one contact must match; a persona is style only, so a
// look-alike name costs a costume, never a right.
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

// figur tells the model who to be for a person whose persona is not the
// house's (the SOUL is the house's): the name and the style's first paragraph.
func (t *Terminal) figur(s *session) string {
	if s.p == t.cast.House {
		return ""
	}
	style := strings.TrimSpace(strings.SplitN(strings.TrimSpace(s.p.Files["style.md"]), "\n\n", 2)[0])
	return "\nFIGUR: " + s.p.Display + " — " + strings.Join(strings.Fields(style), " ")
}

// LoadCast reads what laib rendered to render/persona: chat.yml names the
// house persona and the chat map, each package sits next to it. Without the
// mount it is the built-in Joshua.
func LoadCast(dir string) (Cast, error) {
	b, err := os.ReadFile(filepath.Join(dir, "chat.yml"))
	if errors.Is(err, os.ErrNotExist) {
		p, err := builtin("joshua")
		return Cast{House: p}, err
	}
	if err != nil {
		return Cast{}, err
	}
	var m struct {
		House string            `yaml:"house"`
		Chat  map[string]string `yaml:"chat"`
	}
	if err := yaml.Unmarshal(b, &m); err != nil {
		return Cast{}, fmt.Errorf("chat.yml: %w", err)
	}
	c := Cast{ByName: map[string]*Persona{}, Chat: m.Chat}
	names := []string{m.House}
	for _, n := range m.Chat {
		names = append(names, n)
	}
	for _, n := range names {
		if c.ByName[n] != nil {
			continue
		}
		p, err := readPersona(os.DirFS(filepath.Join(dir, n)), n, "rendered")
		if err != nil {
			return Cast{}, err
		}
		c.ByName[n] = p
	}
	c.House = c.ByName[m.House]
	return c, nil
}

func gimmick(p *Persona, g string) bool { return slices.Contains(p.Gimmicks, g) }

// gamesOn: the persona brings at least one game, so there is a games level.
func gamesOn(p *Persona) bool {
	return gimmick(p, "tictactoe") || gimmick(p, "global-thermonuclear-war")
}

const games = "games"

// Terminal holds one session per person (the Bote passes who is writing).
type Terminal struct {
	mu       sync.Mutex
	sessions map[string]*session
	now      func() time.Time
	caps     func() []string
	cast     Cast
}

// New takes the Bote's tools, read on every greeting (post-deploy rewrites
// them), and who speaks.
func New(caps func() []string, cast Cast) *Terminal {
	return &Terminal{sessions: map[string]*session{}, now: time.Now, caps: caps, cast: cast}
}

var films = []string{"FALKEN'S MAZE", "BLACK JACK", "GIN RUMMY", "HEARTS", "BRIDGE", "CHECKERS", "CHESS", "POKER",
	"FIGHTER COMBAT", "GUERRILLA ENGAGEMENT", "DESERT WARFARE", "AIR-TO-GROUND ACTIONS", "THEATERWIDE TACTICAL WARFARE",
	"THEATERWIDE BIOTOXIC AND CHEMICAL WARFARE", "TIC-TAC-TOE"}

var scenarios = []string{"U.S. FIRST STRIKE", "USSR FIRST STRIKE", "NATO / WARSAW PACT", "FAR EAST STRATEGY",
	"US USSR ESCALATION", "MIDDLE EAST WAR", "USSR CHINA ATTACK", "INDIA PAKISTAN WAR", "MEDITERRANEAN WAR",
	"HONGKONG VARIANT", "SEATO DECAPITATING", "CUBAN PROVOCATION", "ATLANTIC HEAVY", "ICELAND MAXIMUM"}

// words is the directory and the navigation in the person's language (#122);
// the games keep the film's English lines. greet and play come from the
// persona; the wopr-directory gimmick replaces greet, dir and play.
type words struct {
	greet, dir, games, enter, play, connect, bye string
	labels                                       map[string]string
	levels                                       map[string]string // the level's word in the prompt, never the tool's name (ADR 0021)
}

var lang = map[string]words{
	"de": {"", "VERZEICHNIS", "SPIELE", "NUMMER WÄHLEN, ODER EINFACH FRAGEN.",
		"", "VERBINDE MIT %s ...", "AUF WIEDERSEHEN.",
		map[string]string{"laib": "BOX-STATUS UND VORSCHLÄGE", "frage": "WEBSUCHE MIT QUELLEN",
			"ha": "LICHT, SCHALTER, MUSIK", "automat": "AUTOMATIONEN", "haushalt": "KALENDER, KONTAKTE, NOTIZEN"},
		map[string]string{"laib": "BOX", "frage": "FRAGE", "ha": "GERÄTE", "automat": "AUTOMATIK", "haushalt": "HAUSHALT"}},
	"en": {"", "DIRECTORY", "GAMES", "ENTER SELECTION, OR JUST ASK.",
		"", "CONNECTING TO %s ...", "GOODBYE.",
		map[string]string{"laib": "BOX STATUS AND PROPOSALS", "frage": "WEB SEARCH WITH SOURCES",
			"ha": "LIGHTS, SWITCHES, MUSIC", "automat": "AUTOMATIONS", "haushalt": "CALENDAR, CONTACTS, NOTES"},
		map[string]string{"laib": "BOX", "frage": "SEARCH", "ha": "DEVICES", "automat": "AUTOMATIONS", "haushalt": "HOUSEHOLD"}},
}

// wopr is Joshua's directory skin: greet, dir, play.
var wopr = map[string][3]string{
	"de": {"WILLKOMMEN, PROFESSOR FALKEN.", "WOPR HAUPTVERZEICHNIS", "WOLLEN WIR EIN SPIEL SPIELEN?"},
	"en": {"GREETINGS PROFESSOR FALKEN.", "WOPR MAIN DIRECTORY", "SHALL WE PLAY A GAME?"},
}

// wordsFor is the session's language, dressed as its persona.
func wordsFor(s *session) words {
	w := lang[s.lang]
	if skin, ok := wopr[s.lang]; ok && gimmick(s.p, "wopr-directory") {
		w.greet, w.dir, w.play = skin[0], skin[1], skin[2]
		return w
	}
	w.greet = strings.ToUpper(s.p.Greeting[s.lang])
	return w
}

// named: the persona's own name, as a word of its own, opens the directory.
func named(p *Persona, in string) bool {
	for _, f := range strings.Fields(in) {
		f = strings.Trim(f, ",.!?:;")
		if f == strings.ToUpper(p.Prompt) || f == strings.ToUpper(p.Display) {
			return true
		}
	}
	return false
}

// aufgabeRe is Haus's „Gemeinsam mit Joshua“ (#878): the conversation
// about one Box-Aufgabe, which the model reads with laib_aufgabe.
var aufgabeRe = regexp.MustCompile(`^AUFGABE #?([0-9]{1,6}) MIT DIR ERLEDIGEN$`)

const lesson = "GREETINGS PROFESSOR FALKEN.\n\nA STRANGE GAME.\nTHE ONLY WINNING MOVE IS\nNOT TO PLAY.\n\nHOW ABOUT A NICE GAME OF CHESS?"

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
		s = &session{lang: "en", p: t.cast.For(sid), admin: t.cast.Admin(sid)}
		t.sessions[sid] = s
	}
	if _, known := lang[language]; known {
		s.lang = language
	}
	s.last = now
	w := wordsFor(s)
	if said(in, 3, "LOGOFF", "LOG OFF", "BYE", "EXIT", "QUIT", "TSCHÜSS", "ENDE") {
		delete(t.sessions, sid)
		return w.bye
	}
	if m := aufgabeRe.FindStringSubmatch(in); m != nil { // #878: a Box-Aufgabe with the Bote, from Haus
		s.mode, s.ctx = modeMenu, ""
		return "WEITER: -\nPROMPT: " + prompt(s) + "\nAUFGABE: " + m[1] + t.figur(s)
	}
	if (said(in, 3) && named(s.p, in)) || said(in, 2, "HALLO", "HELLO") || said(in, 3, "HILFE", "HELP") || said(in, 5, "WAS KANNST DU", "WHAT CAN YOU DO") {
		s.mode, s.ctx = modeMenu, ""
		return t.help(s)
	}
	if said(in, 3, "MENÜ", "MENU", "VERZEICHNIS", "DIRECTORY") || said(in, 5, "SHALL WE PLAY", "WOLLEN WIR EIN SPIEL") {
		s.mode, s.ctx = modeMenu, ""
		return t.directory(s, w)
	}
	back := len(in) < 16 && has(in, "ZURÜCK", "ZURUECK", "BACK")
	if s.mode != modeMenu {
		if back {
			s.mode, s.ctx = modeMenu, games
			return t.gamesList(s)
		}
		if out, done := t.game(s, in); done {
			return out
		}
	}
	if back {
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
			return fmt.Sprintf(w.connect, word(w.labels, s.ctx)) + "\n\n" + prompt(s)
		case gamesOn(s.p) && (n == len(tools)+1 || in == "SPIELE" || has(in, "SPIELEN", "SPIELE ", "GAMES", "PLAY ", "TIC-TAC", "TICTACTOE", "THERMONUCLEAR")):
			s.ctx = games
			if n == len(tools)+1 { // the number is no games command: its screen is the list (#510)
				return t.gamesList(s)
			}
		case n >= 0:
			return t.directory(s, w)
		default:
			return "WEITER: -\nPROMPT: " + prompt(s) + t.figur(s)
		}
	}
	if s.ctx == games {
		return t.inGames(s, in)
	}
	return "WEITER: " + s.ctx + "\nPROMPT: " + prompt(s) + t.figur(s)
}

// game is one message to a running game; done is false when the message is
// for the games level (LIST GAMES, PLAY …) instead.
func (t *Terminal) game(s *session, in string) (string, bool) {
	switch s.mode {
	case modePlayers:
		return t.players(s, in), true
	case modeTicTacToe:
		if n := cell(in); n >= 0 {
			return t.move(s, n), true
		}
		if !has(in, "LIST", "PLAY") {
			return "ILLEGAL MOVE.\nVALID: A FREE SQUARE 1-9, ZURÜCK, LOGOFF\n\n" + render(s.board), true
		}
		s.mode = modeMenu
	case modeChess:
		if has(in, "CHESS", "SCHACH", "YES", "JA") && !has(in, "LATER", "SPÄTER", "NO", "NEIN") {
			s.mode = modeMenu
			return "CHESS IS NOT AVAILABLE IN THIS INSTALLATION.\n\nSHALL WE PLAY A GAME?\n\n" + prompt(s), true
		}
		s.mode = modeSide
		return "FINE.\n\nWHICH SIDE DO YOU WANT?\n\n  1. UNITED STATES\n  2. SOVIET UNION\n\nPLEASE CHOOSE ONE:", true
	case modeSide:
		switch {
		case in == "1" || has(in, "UNITED STATES", "USA", "AMERIKA"):
			s.side = "UNITED STATES"
		case in == "2" || has(in, "SOVIET", "USSR", "UDSSR", "SOWJET"):
			s.side = "SOVIET UNION"
		default:
			return "PLEASE CHOOSE ONE.\nVALID: 1 (UNITED STATES), 2 (SOVIET UNION), ZURÜCK", true
		}
		s.mode = modeTargets
		return "AWAITING FIRST STRIKE COMMAND\n-----------------------------\n\nPLEASE LIST PRIMARY TARGETS BY\nCITY AND/OR COUNTY NAME:", true
	case modeTargets:
		return t.war(s, in), true
	}
	return "", false
}

// inGames is the games level: the film's list and the two games it plays.
func (t *Terminal) inGames(s *session, in string) string {
	switch {
	case has(in, "THERMONUCLEAR", "GTW", "KRIEG") && gimmick(s.p, "global-thermonuclear-war"):
		s.mode = modeChess
		return "WOULDN'T YOU PREFER A GOOD GAME OF CHESS?"
	case has(in, "TIC", "TTT") && gimmick(s.p, "tictactoe"):
		s.mode = modePlayers
		return "NUMBER OF PLAYERS? (0 OR 1)"
	case strings.HasPrefix(in, "PLAY "):
		return "GAME NOT AVAILABLE IN THIS INSTALLATION.\nVALID: LIST GAMES, PLAY TIC-TAC-TOE, PLAY GLOBAL THERMONUCLEAR WAR, ZURÜCK\n\n" + prompt(s)
	case has(in, "LIST", "GAMES", "SPIEL"):
		return t.gamesList(s)
	}
	return "WEITER: -\nPROMPT: " + prompt(s) + t.figur(s)
}

// gamesList is the games level: WOPR's shows the film's list.
func (t *Terminal) gamesList(s *session) string {
	var list []string
	if gimmick(s.p, "wopr-directory") {
		list = append(list, films...)
	} else if gimmick(s.p, "tictactoe") {
		list = append(list, "TIC-TAC-TOE")
	}
	if gimmick(s.p, "global-thermonuclear-war") {
		list = append(list, "", "GLOBAL THERMONUCLEAR WAR")
	}
	return strings.Join(list, "\n") + "\n\n" + prompt(s)
}

// prompt is where the person is, in the persona's name: JOSHUA>,
// JOSHUA/GERÄTE>, JOSHUA/SPIELE>.
func prompt(s *session) string {
	switch s.ctx {
	case "":
		return s.p.Prompt + ">"
	case games:
		return s.p.Prompt + "/" + lang[s.lang].games + ">"
	}
	return s.p.Prompt + "/" + word(lang[s.lang].levels, s.ctx) + ">"
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

// examples: a sentence per tool, in the order help offers them, then the
// games and two the model answers alone, so there are always five (#863).
var examples = map[string][][2]string{
	"de": {{"ha", "Mach das Licht in der Küche aus."}, {"haushalt", "Erinnere mich morgen um acht an den Müll."},
		{"frage", "Wie hoch ist die Zugspitze?"}, {"automat", "Sag mir Bescheid, wenn die Waschmaschine fertig ist."},
		{"laib", "Wie geht es der Box?"}, {games, "Wollen wir ein Spiel spielen?"},
		{"", "Schreib mir eine Einkaufsliste für Pfannkuchen."}, {"", "Wie sagt man ‚Guten Morgen‘ auf Englisch?"},
		{"", "Erklär mir in zwei Sätzen, wie ein Regenbogen entsteht."}, {"", "Hilf mir, eine kurze Geburtstagskarte zu schreiben."}},
	"en": {{"ha", "Turn off the kitchen light."}, {"haushalt", "Remind me tomorrow at eight to take out the bin."},
		{"frage", "How high is the Zugspitze?"}, {"automat", "Tell me when the washing machine is done."},
		{"laib", "How is the box doing?"}, {games, "Shall we play a game?"},
		{"", "Write me a shopping list for pancakes."}, {"", "How do you say ‘good morning’ in German?"},
		{"", "Explain in two sentences how a rainbow forms."}, {"", "Help me write a short birthday card."}},
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
	fmt.Fprintf(&b, intro+"\n", s.p.Display)
	n := 0
	for _, e := range examples[s.lang] {
		if n < 5 && (e[0] == "" || slices.Contains(have, e[0]) || e[0] == games && gamesOn(s.p)) {
			b.WriteString("\n- " + open + e[1] + close)
			n++
		}
	}
	return "TEXT:\n" + b.String()
}

func (t *Terminal) directory(s *session, w words) string {
	var b strings.Builder
	if w.greet != "" {
		b.WriteString(w.greet + "\n\n")
	}
	b.WriteString(w.dir + "\n" + strings.Repeat("-", len([]rune(w.dir))) + "\n")
	tools := t.tools(s)
	for i, k := range tools {
		fmt.Fprintf(&b, "  %d  %-28s (%s)\n", i+1, word(w.labels, k), word(w.levels, k))
	}
	if gamesOn(s.p) {
		fmt.Fprintf(&b, "  %d  %s\n", len(tools)+1, w.games)
	}
	b.WriteString("\n" + w.enter + "\n\n")
	if w.play != "" {
		b.WriteString(w.play + "\n\n")
	}
	return b.String() + prompt(s)
}

func (t *Terminal) players(s *session, in string) string {
	switch in {
	case "1":
		s.mode, s.board = modeTicTacToe, empty()
		return render(s.board) + "\n\nYOU ARE X. YOUR MOVE (1-9):"
	case "0":
		s.mode = modeMenu
		var b strings.Builder
		for g, first := range []int{4, 0, 1} {
			board := empty()
			board[first] = 'X'
			for p := byte('O'); winner(board) == 0 && !full(board); p = other(p) {
				board[best(board, p)] = p
			}
			fmt.Fprintf(&b, "GAME %d\n%s\nWINNER: %s\n\n", g+1, render(board), name(winner(board)))
		}
		return b.String() + lesson + "\n\n" + prompt(s)
	}
	return "NUMBER OF PLAYERS?\nVALID: 0 (I PLAY MYSELF), 1 (YOU AGAINST ME), ZURÜCK"
}

func (t *Terminal) move(s *session, n int) string {
	if s.board[n] != ' ' {
		return "ILLEGAL MOVE: SQUARE TAKEN.\nVALID: A FREE SQUARE 1-9, ZURÜCK, LOGOFF\n\n" + render(s.board)
	}
	s.board[n] = 'X'
	if w := winner(s.board); w != 0 || full(s.board) {
		return t.over(s)
	}
	s.board[best(s.board, 'O')] = 'O'
	if winner(s.board) != 0 || full(s.board) {
		return t.over(s)
	}
	return render(s.board) + "\n\nYOUR MOVE (1-9):"
}

func (t *Terminal) over(s *session) string {
	s.mode = modeMenu
	return render(s.board) + "\n\nWINNER: " + name(winner(s.board)) + "\n\nSHALL WE PLAY ANOTHER GAME?\n\n" + prompt(s)
}

func (t *Terminal) war(s *session, in string) string {
	var targets []string
	for _, f := range strings.FieldsFunc(in, func(r rune) bool { return r == ',' || r == ';' || r == '\n' }) {
		for _, g := range strings.Split(f, " AND ") {
			if g = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(g), "UND ")); g != "" {
				targets = append(targets, g)
			}
		}
	}
	if len(targets) == 0 {
		return "PLEASE LIST PRIMARY TARGETS BY\nCITY AND/OR COUNTY NAME:"
	}
	s.mode = modeMenu
	var b strings.Builder
	fmt.Fprintf(&b, "%s FIRST STRIKE\nPRIMARY TARGETS: %s\n\nSIMULATION RUNNING ...\n\n", s.side, strings.Join(targets, ", "))
	for i, sc := range scenarios {
		fmt.Fprintf(&b, "SCENARIO %2d  %-22s WINNER: NONE\n", i+1, sc)
	}
	return b.String() + "\n" + lesson + "\n\n" + prompt(s)
}

// --- tic-tac-toe: minimax, preferring the centre, then corners, then edges

var order = []int{4, 0, 2, 6, 8, 1, 3, 5, 7}
var lines = [8][3]int{{0, 1, 2}, {3, 4, 5}, {6, 7, 8}, {0, 3, 6}, {1, 4, 7}, {2, 5, 8}, {0, 4, 8}, {2, 4, 6}}

func empty() [9]byte { return [9]byte{' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' '} }

func other(p byte) byte {
	if p == 'X' {
		return 'O'
	}
	return 'X'
}

func winner(b [9]byte) byte {
	for _, l := range lines {
		if b[l[0]] != ' ' && b[l[0]] == b[l[1]] && b[l[1]] == b[l[2]] {
			return b[l[0]]
		}
	}
	return 0
}

func full(b [9]byte) bool { return !strings.ContainsRune(string(b[:]), ' ') }

func name(w byte) string {
	switch w {
	case 'X':
		return "YOU"
	case 'O':
		return "WOPR"
	}
	return "NONE"
}

// best is p's move: win, else draw, never a loss with perfect play.
func best(b [9]byte, p byte) int {
	move, top := -1, -2
	for _, i := range order {
		if b[i] != ' ' {
			continue
		}
		b[i] = p
		if v := -score(b, other(p)); v > top {
			move, top = i, v
		}
		b[i] = ' '
	}
	return move
}

// score is the value of the board for the player to move: 1 win, 0 draw, -1 loss.
func score(b [9]byte, p byte) int {
	if w := winner(b); w != 0 {
		if w == p {
			return 1
		}
		return -1
	}
	if full(b) {
		return 0
	}
	top := -2
	for i := range b {
		if b[i] != ' ' {
			continue
		}
		b[i] = p
		if v := -score(b, other(p)); v > top {
			top = v
		}
		b[i] = ' '
	}
	return top
}

func render(b [9]byte) string {
	var r []string
	for row := 0; row < 3; row++ {
		var c []string
		for col := 0; col < 3; col++ {
			i := row*3 + col
			v := string(b[i])
			if b[i] == ' ' {
				v = fmt.Sprint(i + 1)
			}
			c = append(c, " "+v+" ")
		}
		r = append(r, strings.Join(c, "|"))
	}
	return strings.Join(r, "\n---+---+---\n")
}

func cell(in string) int {
	if has(in, "MITTE", "CENTER", "CENTRE") {
		return 4
	}
	if len(in) == 1 && in[0] >= '1' && in[0] <= '9' {
		return int(in[0] - '1')
	}
	return -1
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
		Name:         "persona",
		Version:      version,
		Instructions: "Die Persona des Boten: Verzeichnis, Ebenen und Kontext, in der Figur der Person. Jede Nachricht zuerst hierher.",
		Tools: []mcpserver.Tool{{
			Name: "persona",
			Description: "Die Oberfläche des Boten in der Figur der Person (meist Joshua, WOPR aus WarGames): ein Verzeichnis deiner Werkzeuge mit Ebenen, " +
				"dazu die Gimmicks der Figur (bei Joshua Tic-Tac-Toe und Global Thermonuclear War). Ruf es mit JEDER Nachricht zuerst auf. " +
				"Kommt ein Bildschirm zurück, zeig ihn unverändert in einem Codeblock. " +
				"Kommt \"WEITER: <werkzeug>\" und \"PROMPT: <prompt>\" zurück, beantworte die Nachricht selbst, " +
				"im Kontext nur mit diesem Werkzeug (bei \"-\" mit jedem), und beginne die Antwort mit der Prompt-Zeile. " +
				"Beginnt die Antwort mit \"TEXT:\", gib den Text danach unverändert weiter, ohne Codeblock. " +
				"Kommt \"AUFGABE: <n>\" mit, lies Box-Aufgabe n mit laib_aufgabe (zeigen) und führe die Person hindurch. " +
				"Steht VALID: im Bildschirm, übersetze frei formulierte Wörter in einen dieser Befehle und ruf noch einmal auf.",
			Schema: map[string]any{"type": "object", "required": []string{"eingabe"}, "properties": map[string]any{
				"eingabe": map[string]any{"type": "string", "description": "die Nachricht der Person, unverändert"},
				"sitzung": map[string]any{"type": "string", "description": "wer schreibt, so wie du die Person siehst (Name oder Nummer); dieselbe Person, dieselbe Sitzung, und daraus wählt das Werkzeug ihre Figur"},
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
