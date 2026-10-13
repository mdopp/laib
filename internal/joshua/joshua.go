// Package joshua is an easter egg for the Bote (#119): WOPR from WarGames as
// a small terminal. It lists what the Bote can do, then the film's games, and
// plays two of them for real: tic-tac-toe (it never loses) and Global
// Thermonuclear War (nobody wins). It reads nothing from the box and changes
// nothing; the model only passes text through, the game is this code.
package joshua

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mdopp/laib/internal/mcpserver"
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
}

const games = "games"

// Terminal holds one session per person (the Bote passes who is writing).
type Terminal struct {
	mu       sync.Mutex
	sessions map[string]*session
	now      func() time.Time
	caps     func() []string
}

// New takes the Bote's tools, read on every greeting (post-deploy rewrites them).
func New(caps func() []string) *Terminal {
	return &Terminal{sessions: map[string]*session{}, now: time.Now, caps: caps}
}

var films = []string{"FALKEN'S MAZE", "BLACK JACK", "GIN RUMMY", "HEARTS", "BRIDGE", "CHECKERS", "CHESS", "POKER",
	"FIGHTER COMBAT", "GUERRILLA ENGAGEMENT", "DESERT WARFARE", "AIR-TO-GROUND ACTIONS", "THEATERWIDE TACTICAL WARFARE",
	"THEATERWIDE BIOTOXIC AND CHEMICAL WARFARE", "TIC-TAC-TOE"}

var scenarios = []string{"U.S. FIRST STRIKE", "USSR FIRST STRIKE", "NATO / WARSAW PACT", "FAR EAST STRATEGY",
	"US USSR ESCALATION", "MIDDLE EAST WAR", "USSR CHINA ATTACK", "INDIA PAKISTAN WAR", "MEDITERRANEAN WAR",
	"HONGKONG VARIANT", "SEATO DECAPITATING", "CUBAN PROVOCATION", "ATLANTIC HEAVY", "ICELAND MAXIMUM"}

// words is the directory and the navigation in the person's language (#122);
// the games keep the film's English lines.
type words struct {
	greet, dir, games, enter, play, connect, bye string
	labels                                       map[string]string
}

var lang = map[string]words{
	"de": {"WILLKOMMEN, PROFESSOR FALKEN.", "WOPR HAUPTVERZEICHNIS", "SPIELE", "NUMMER WÄHLEN, ODER EINFACH FRAGEN.",
		"WOLLEN WIR EIN SPIEL SPIELEN?", "VERBINDE MIT %s ...", "AUF WIEDERSEHEN.",
		map[string]string{"laib": "BOX-STATUS UND VORSCHLÄGE", "frage": "WEBSUCHE MIT QUELLEN",
			"haus": "LICHT, SCHALTER, MUSIK", "automat": "AUTOMATIONEN", "haushalt": "KALENDER, KONTAKTE, NOTIZEN"}},
	"en": {"GREETINGS PROFESSOR FALKEN.", "WOPR MAIN DIRECTORY", "GAMES", "ENTER SELECTION, OR JUST ASK.",
		"SHALL WE PLAY A GAME?", "CONNECTING TO %s ...", "GOODBYE.",
		map[string]string{"laib": "BOX STATUS AND PROPOSALS", "frage": "WEB SEARCH WITH SOURCES",
			"haus": "LIGHTS, SWITCHES, MUSIC", "automat": "AUTOMATIONS", "haushalt": "CALENDAR, CONTACTS, NOTES"}},
}

const lesson = "GREETINGS PROFESSOR FALKEN.\n\nA STRANGE GAME.\nTHE ONLY WINNING MOVE IS\nNOT TO PLAY.\n\nHOW ABOUT A NICE GAME OF CHESS?"

// Input is Talk in the session's language (English for a new one).
func (t *Terminal) Input(sid, line string) string { return t.Talk(sid, "", line) }

// Talk is one message: a screen to show as is (its last line is the prompt),
// or "WEITER: <tool>\nPROMPT: <prompt>" when it is a request for the Bote in
// the current context ("-" at the top: any tool).
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
		s = &session{lang: "en"}
		t.sessions[sid] = s
	}
	if _, known := lang[language]; known {
		s.lang = language
	}
	s.last = now
	w := lang[s.lang]
	if has(in, "LOGOFF", "LOG OFF", "BYE", "EXIT", "QUIT", "TSCHÜSS", "ENDE") {
		delete(t.sessions, sid)
		return w.bye
	}
	if has(in, "JOSHUA", "MENÜ", "MENU", "VERZEICHNIS", "DIRECTORY", "HILFE", "HELP", "WAS KANNST DU", "WHAT CAN YOU DO",
		"SHALL WE PLAY", "WOLLEN WIR EIN SPIEL", "HALLO", "HELLO") {
		s.mode, s.ctx = modeMenu, ""
		return t.directory(w)
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
		return t.directory(w)
	}
	tools := t.tools()
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
			return fmt.Sprintf(w.connect, label(w, s.ctx)) + "\n\n" + prompt(s)
		case n == len(tools)+1 || in == "SPIELE" || has(in, "SPIELEN", "SPIELE ", "GAMES", "PLAY ", "TIC-TAC", "TICTACTOE", "THERMONUCLEAR"):
			s.ctx = games
		case n >= 0:
			return t.directory(w)
		default:
			return "WEITER: -\nPROMPT: " + prompt(s)
		}
	}
	if s.ctx == games {
		return t.inGames(s, in)
	}
	return "WEITER: " + s.ctx + "\nPROMPT: " + prompt(s)
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
	case has(in, "THERMONUCLEAR", "GTW", "KRIEG"):
		s.mode = modeChess
		return "WOULDN'T YOU PREFER A GOOD GAME OF CHESS?"
	case has(in, "TIC", "TTT"):
		s.mode = modePlayers
		return "NUMBER OF PLAYERS? (0 OR 1)"
	case strings.HasPrefix(in, "PLAY "):
		return "GAME NOT AVAILABLE IN THIS INSTALLATION.\nVALID: LIST GAMES, PLAY TIC-TAC-TOE, PLAY GLOBAL THERMONUCLEAR WAR, ZURÜCK\n\n" + prompt(s)
	case has(in, "LIST", "GAMES", "SPIEL"):
		return t.gamesList(s)
	}
	return "WEITER: -\nPROMPT: " + prompt(s)
}

func (t *Terminal) gamesList(s *session) string {
	return strings.Join(films, "\n") + "\n\nGLOBAL THERMONUCLEAR WAR\n\n" + prompt(s)
}

// prompt is where the person is: JOSHUA>, JOSHUA/HAUS>, JOSHUA/SPIELE>.
func prompt(s *session) string {
	switch s.ctx {
	case "":
		return "JOSHUA>"
	case games:
		return "JOSHUA/" + lang[s.lang].games + ">"
	}
	return "JOSHUA/" + strings.ToUpper(s.ctx) + ">"
}

func (t *Terminal) tools() []string {
	if t.caps == nil {
		return nil
	}
	return t.caps()
}

func label(w words, k string) string {
	if l, ok := w.labels[k]; ok {
		return l
	}
	return strings.ToUpper(k)
}

func (t *Terminal) directory(w words) string {
	var b strings.Builder
	b.WriteString(w.greet + "\n\n" + w.dir + "\n" + strings.Repeat("-", len([]rune(w.dir))) + "\n")
	tools := t.tools()
	for i, k := range tools {
		fmt.Fprintf(&b, "  %d  %-28s (%s)\n", i+1, label(w, k), strings.ToUpper(k))
	}
	fmt.Fprintf(&b, "  %d  %s\n\n%s\n\n%s\n\nJOSHUA>", len(tools)+1, w.games, w.enter, w.play)
	return b.String()
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

func has(in string, words ...string) bool {
	for _, w := range words {
		if strings.Contains(in, w) {
			return true
		}
	}
	return false
}

// clone copies one session under another id; the tests walk every line of play with it.
func (t *Terminal) clone(from, to string) *Terminal {
	c := New(t.caps)
	c.now = t.now
	if s, ok := t.sessions[from]; ok {
		cp := *s
		c.sessions[to] = &cp
	}
	return c
}

// Server is the MCP tool the Bote calls with every message (#122): it knows
// where the person is and answers with a screen or with WEITER.
func Server(t *Terminal, version string) *mcpserver.Server {
	return &mcpserver.Server{
		Name:         "joshua",
		Version:      version,
		Instructions: "Joshua, die Persona des Boten: Verzeichnis, Ebenen und Kontext. Jede Nachricht zuerst hierher.",
		Tools: []mcpserver.Tool{{
			Name: "joshua",
			Description: "Joshua (WOPR aus WarGames), die Oberfläche des Boten: ein Verzeichnis deiner Werkzeuge mit Ebenen, " +
				"dazu Tic-Tac-Toe und Global Thermonuclear War. Ruf es mit JEDER Nachricht zuerst auf. " +
				"Kommt ein Bildschirm zurück, zeig ihn unverändert in einem Codeblock. " +
				"Kommt \"WEITER: <werkzeug>\" und \"PROMPT: <prompt>\" zurück, beantworte die Nachricht selbst, " +
				"im Kontext nur mit diesem Werkzeug (bei \"-\" mit jedem), und beginne die Antwort mit der Prompt-Zeile. " +
				"Steht VALID: im Bildschirm, übersetze frei formulierte Wörter in einen dieser Befehle und ruf noch einmal auf.",
			Schema: map[string]any{"type": "object", "required": []string{"eingabe"}, "properties": map[string]any{
				"eingabe": map[string]any{"type": "string", "description": "die Nachricht der Person, unverändert"},
				"sitzung": map[string]any{"type": "string", "description": "wer schreibt (Name oder Nummer); dieselbe Person, dieselbe Sitzung"},
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
