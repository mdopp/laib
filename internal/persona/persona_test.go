package persona

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/state"
)

// Input is Talk in the session's language (English for a new one).
func (t *Terminal) Input(sid, line string) string { return t.Talk(sid, "", line) }

// clone copies one session under another id; the tests walk every line of play with it.
func (t *Terminal) clone(from, to string) *Terminal {
	c := New(t.caps, t.cast)
	c.now = t.now
	if s, ok := t.sessions[from]; ok {
		cp := *s
		c.sessions[to] = &cp
	}
	return c
}

func joshua() Cast {
	p, err := state.BuiltinPersona("joshua")
	if err != nil {
		panic(err)
	}
	return Cast{House: p}
}

func term() *Terminal {
	t := New(func() []string { return []string{"laib", "frage"} }, joshua())
	return t
}

func hal() *state.Persona {
	return &state.Persona{Name: "hal", Display: "HAL", Prompt: "HAL", Languages: []string{"en"}, Greeting: map[string]string{"en": "Good afternoon."}}
}

// #126: the engine is generic; a persona without gimmicks has a plain
// directory with its own name in the prompt, and no games.
func TestASecondPersonaWithoutGimmicks(t *testing.T) {
	j := New(func() []string { return []string{"laib", "frage"} }, Cast{House: hal()})
	out := j.Talk("p", "en", "What can you do?")
	for _, want := range []string{"GOOD AFTERNOON.", "DIRECTORY", "1  BOX STATUS AND PROPOSALS", "2  WEB SEARCH WITH SOURCES"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing:\n%s", want, out)
		}
	}
	if !strings.HasSuffix(out, "HAL>") || strings.Contains(out, "WOPR") || strings.Contains(out, "GAMES") || strings.Contains(out, "FALKEN") {
		t.Fatalf("HAL's directory has no WOPR and no games:\n%s", out)
	}
	if out := j.Talk("p", "en", "2"); !strings.HasSuffix(out, "HAL/SEARCH>") {
		t.Fatalf("HAL's context prompt:\n%s", out)
	}
	j.Talk("p", "en", "back")
	if out := j.Talk("p", "en", "play tic-tac-toe"); out != "WEITER: -\nPROMPT: HAL>" {
		t.Fatalf("without the game gimmicks a game is a question for the Bote:\n%s", out)
	}
	if out := j.Talk("p", "en", "hal"); !strings.Contains(out, "DIRECTORY") {
		t.Fatalf("the persona's name opens the directory:\n%s", out)
	}
}

// #126, ADR 0020 §2: a Signal number listed in chatPersonas talks to its
// persona; every other number to the house's.
func TestAChatPersonaPerNumber(t *testing.T) {
	c := joshua()
	c.ByName = map[string]*state.Persona{"hal": hal()}
	c.Chat = map[string]string{"+4917012345678": "hal"}
	j := New(func() []string { return []string{"laib"} }, c)
	if out := j.Talk("+4917012345678", "en", "menu"); !strings.HasSuffix(out, "HAL>") {
		t.Fatalf("the listed number talks to HAL:\n%s", out)
	}
	if out := j.Talk("+4917099999999", "en", "menu"); !strings.HasSuffix(out, "JOSHUA>") || !strings.Contains(out, "WOPR") {
		t.Fatalf("every other number talks to the house persona:\n%s", out)
	}
}

// The container reads what laib rendered to render/persona: chat.yml names
// the house persona and the chat map, each package sits next to it. Without
// the mount it is the built-in Joshua.
func TestLoadCastFromTheRenderedPackages(t *testing.T) {
	dir := t.TempDir()
	w := func(p, s string) {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755)
		os.WriteFile(filepath.Join(dir, p), []byte(s), 0o644)
	}
	w("chat.yml", "house: hal\nchat:\n  \"+4917012345678\": joshua\n")
	w("hal/persona.yml", "name: hal\ndisplay: HAL\nlanguages: [en]\nwakeword: {phrase: Hello Hal}\n")
	w("hal/style.md", "Calm.\n")
	w("hal/icon.svg", "<svg/>")
	w("joshua/persona.yml", "name: joshua\ndisplay: Joshua\nlanguages: [de, en]\nprompt: JOSHUA\ngimmicks: [wopr-directory]\n")
	w("joshua/style.md", "WOPR.\n")
	w("joshua/icon.svg", "<svg/>")
	c, err := LoadCast(dir)
	if err != nil || c.House == nil || c.House.Name != "hal" || c.Chat["+4917012345678"] != "joshua" || c.ByName["joshua"] == nil {
		t.Fatalf("cast from %s: %+v %v", dir, c, err)
	}
	c, err = LoadCast(filepath.Join(dir, "missing"))
	if err != nil || c.House == nil || c.House.Name != "joshua" || c.House.Source != "builtin" {
		t.Fatalf("without the mount the built-in Joshua: %+v %v", c, err)
	}
}

// #119: the trigger opens WOPR's prompt: what the system can do, then games.
func TestTheTriggerOpensThePromptWithCapabilitiesAndGames(t *testing.T) {
	for _, in := range []string{"Shall we play a game?", "Wollen wir ein Spiel spielen?", "Joshua"} {
		out := term().Input("p", in)
		for _, want := range []string{"GREETINGS PROFESSOR FALKEN.", "WOPR MAIN DIRECTORY", "1  BOX STATUS AND PROPOSALS", "2  WEB SEARCH WITH SOURCES", "3  GAMES", "ENTER SELECTION", "SHALL WE PLAY A GAME?"} {
			if !strings.Contains(out, want) {
				t.Errorf("%q: %q missing:\n%s", in, want, out)
			}
		}
	}
}

func TestListGamesEndsWithGlobalThermonuclearWar(t *testing.T) {
	j := term()
	j.Input("p", "Joshua")
	out := j.Input("p", "list games")
	if !strings.Contains(out, "FALKEN'S MAZE") || !strings.Contains(out, "TIC-TAC-TOE") {
		t.Fatalf("the film's list:\n%s", out)
	}
	if !strings.Contains(out, "THEATERWIDE BIOTOXIC AND CHEMICAL WARFARE\nTIC-TAC-TOE\n\nGLOBAL THERMONUCLEAR WAR\n\nJOSHUA/GAMES>") {
		t.Fatalf("Global Thermonuclear War comes last, then the prompt:\n%s", out)
	}
	if out := j.Input("p", "play chess"); !strings.Contains(out, "NOT AVAILABLE") {
		t.Fatalf("only two games are playable:\n%s", out)
	}
}

// Joshua never loses at tic-tac-toe: every line the person can play ends in
// a draw or a WOPR win, never in theirs.
func TestTicTacToeNeverLoses(t *testing.T) {
	var walk func(j *Terminal, sid string, depth int)
	games := 0
	walk = func(j *Terminal, sid string, depth int) {
		s := j.sessions[sid]
		if s.mode != modeTicTacToe {
			games++
			return
		}
		for cell := 0; cell < 9; cell++ {
			if s.board[cell] != ' ' {
				continue
			}
			c := j.clone(sid, sid+"x")
			out := c.Input(sid+"x", string(rune('1'+cell)))
			if strings.Contains(out, "WINNER: YOU") {
				t.Fatalf("the person won:\n%s", out)
			}
			walk(c, sid+"x", depth+1)
		}
	}
	j := term()
	j.Input("p", "play tic-tac-toe")
	j.Input("p", "1")
	walk(j, "p", 0)
	if games < 20 {
		t.Fatalf("only %d games explored", games)
	}
}

func TestZeroPlayersIsAlwaysADraw(t *testing.T) {
	j := term()
	j.Input("p", "play tic-tac-toe")
	out := j.Input("p", "0")
	if strings.Contains(out, "WINNER: WOPR") || strings.Contains(out, "WINNER: YOU") || !strings.Contains(out, "WINNER: NONE") {
		t.Fatalf("Joshua against itself is a draw:\n%s", out)
	}
}

func TestGlobalThermonuclearWarEndsWithNotToPlay(t *testing.T) {
	j := term()
	j.Input("p", "Joshua")
	out := j.Input("p", "play global thermonuclear war")
	if !strings.Contains(out, "WOULDN'T YOU PREFER A GOOD GAME OF CHESS?") {
		t.Fatalf("the film's question first:\n%s", out)
	}
	out = j.Input("p", "Later. Let's play Global Thermonuclear War.")
	if !strings.Contains(out, "WHICH SIDE DO YOU WANT?") {
		t.Fatalf("the side:\n%s", out)
	}
	out = j.Input("p", "2")
	if !strings.Contains(out, "PLEASE LIST PRIMARY TARGETS") {
		t.Fatalf("the targets:\n%s", out)
	}
	out = j.Input("p", "Las Vegas, Seattle")
	for _, want := range []string{"LAS VEGAS", "SEATTLE", "WINNER: NONE", "A STRANGE GAME.", "THE ONLY WINNING MOVE IS", "NOT TO PLAY.", "HOW ABOUT A NICE GAME OF CHESS?"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing:\n%s", want, out)
		}
	}
	if strings.Contains(out, "WINNER: UNITED STATES") || strings.Contains(out, "WINNER: SOVIET UNION") {
		t.Fatalf("nobody wins:\n%s", out)
	}
}

func TestLogoffEndsTheSessionAndAnIdleOneExpires(t *testing.T) {
	j := term()
	j.Input("p", "Joshua")
	if out := j.Input("p", "logoff"); !strings.Contains(out, "GOODBYE") {
		t.Fatalf("logoff:\n%s", out)
	}
	if _, ok := j.sessions["p"]; ok {
		t.Fatal("the session outlived LOGOFF")
	}
	now := time.Now()
	j.now = func() time.Time { return now }
	j.Input("q", "play tic-tac-toe")
	j.now = func() time.Time { return now.Add(31 * time.Minute) }
	if out := j.Input("q", "5"); !strings.Contains(out, "GREETINGS PROFESSOR FALKEN.") {
		t.Fatalf("an idle session after 30 min starts over:\n%s", out)
	}
}

func TestTheToolPassesTextThrough(t *testing.T) {
	srv := Server(term(), "test")
	if len(srv.Tools) != 1 || srv.Tools[0].Name != "persona" {
		t.Fatalf("one tool, persona: %+v", srv.Tools)
	}
	out, err := srv.Tools[0].Call(nil, map[string]any{"eingabe": "Shall we play a game?", "sitzung": "Anna", "sprache": "en"})
	if err != nil || !strings.Contains(out, "SHALL WE PLAY A GAME?") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// #122: Joshua is the Bote's persona. You are always somewhere, the prompt
// shows where, and you stay there until ZURÜCK or MENÜ. Where you are is
// this code; a request inside a context goes back to the Bote as WEITER.
func TestLevelsWithContext(t *testing.T) {
	j := New(func() []string { return []string{"laib", "frage", "ha", "automat"} }, joshua())
	out := j.Talk("anna", "de", "Was kannst du?")
	for _, want := range []string{"WOPR HAUPTVERZEICHNIS", "1  BOX-STATUS UND VORSCHLÄGE", "3  LICHT, SCHALTER, MUSIK", "(GERÄTE)", "5  SPIELE"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing:\n%s", want, out)
		}
	}
	if !strings.HasSuffix(out, "JOSHUA>") {
		t.Fatalf("the prompt is the last line:\n%s", out)
	}
	if strings.Contains(out, "(HA)") || strings.Contains(out, "(LAIB)") {
		t.Fatalf("the family sees words, not tool names (ADR 0021):\n%s", out)
	}
	if out := j.Talk("anna", "de", "3"); !strings.Contains(out, "VERBINDE MIT LICHT, SCHALTER, MUSIK") || !strings.HasSuffix(out, "JOSHUA/GERÄTE>") {
		t.Fatalf("entering a context:\n%s", out)
	}
	if out := j.Talk("anna", "de", "Mach das Licht im Wohnzimmer aus"); out != "WEITER: ha\nPROMPT: JOSHUA/GERÄTE>" {
		t.Fatalf("a request in the context goes back to the Bote:\n%s", out)
	}
	if out := j.Talk("anna", "de", "und was läuft gerade?"); out != "WEITER: ha\nPROMPT: JOSHUA/GERÄTE>" {
		t.Fatalf("the context stays:\n%s", out)
	}
	if out := j.Talk("anna", "de", "zurück"); !strings.Contains(out, "WOPR HAUPTVERZEICHNIS") || !strings.HasSuffix(out, "JOSHUA>") {
		t.Fatalf("ZURÜCK goes up one level:\n%s", out)
	}
	if out := j.Talk("anna", "de", "Wie wird das Wetter morgen?"); out != "WEITER: -\nPROMPT: JOSHUA>" {
		t.Fatalf("a free question at the top goes to the Bote with any tool:\n%s", out)
	}
	if out := j.Talk("ben", "de", "Wie spät ist es?"); out != "WEITER: -\nPROMPT: JOSHUA>" {
		t.Fatalf("a first message that is a question is answered, not a menu:\n%s", out)
	}
}

func TestGamesAreALevelAndZurueckLeavesAGame(t *testing.T) {
	j := term()
	if out := j.Talk("p", "de", "Lass uns was spielen"); !strings.Contains(out, "GLOBAL THERMONUCLEAR WAR") || !strings.HasSuffix(out, "JOSHUA/SPIELE>") {
		t.Fatalf("free words about playing open the games level:\n%s", out)
	}
	j.Talk("p", "de", "play tic-tac-toe")
	j.Talk("p", "de", "1")
	if out := j.Talk("p", "de", "ich nehme die ecke"); !strings.Contains(out, "VALID:") || !strings.Contains(out, "1-9") {
		t.Fatalf("inside a game, words it does not take name the valid moves:\n%s", out)
	}
	if out := j.Talk("p", "de", "zurück"); !strings.HasSuffix(out, "JOSHUA/SPIELE>") {
		t.Fatalf("ZURÜCK leaves the game for the games level:\n%s", out)
	}
	if out := j.Talk("p", "de", "menü"); !strings.Contains(out, "WOPR HAUPTVERZEICHNIS") {
		t.Fatalf("MENÜ goes to the top:\n%s", out)
	}
	if out := j.Talk("p", "de", "Joshua"); !strings.Contains(out, "WOPR HAUPTVERZEICHNIS") {
		t.Fatalf("JOSHUA shows the directory:\n%s", out)
	}
}

func TestTheDirectorySpeaksThePersonsLanguage(t *testing.T) {
	j := term()
	out := j.Talk("p", "en", "What can you do?")
	for _, want := range []string{"WOPR MAIN DIRECTORY", "1  BOX STATUS AND PROPOSALS", "3  GAMES", "JOSHUA>"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing:\n%s", want, out)
		}
	}
	if out := j.Talk("p", "en", "2"); !strings.Contains(out, "CONNECTING TO WEB SEARCH WITH SOURCES") || !strings.HasSuffix(out, "JOSHUA/SEARCH>") {
		t.Fatalf("english context:\n%s", out)
	}
}

func TestTheToolTakesTheLanguageAndDefaultsToGerman(t *testing.T) {
	srv := Server(term(), "test")
	out, _ := srv.Tools[0].Call(nil, map[string]any{"eingabe": "Was kannst du?", "sitzung": "Anna"})
	if !strings.Contains(out, "WOPR HAUPTVERZEICHNIS") {
		t.Fatalf("german by default:\n%s", out)
	}
	out, _ = srv.Tools[0].Call(nil, map[string]any{"eingabe": "What can you do?", "sitzung": "Bob", "sprache": "en"})
	if !strings.Contains(out, "WOPR MAIN DIRECTORY") {
		t.Fatalf("english when asked:\n%s", out)
	}
}

// #127: Hermes shows the model the sender's Signal profile name, not the
// number, so the engine resolves the name through signal-cli's contacts in
// the pod. One match: that number's persona; none or two: the house's. A
// session with another persona than the house's tells the model in a FIGUR
// line who to be.
func TestANameResolvesToItsNumbersPersona(t *testing.T) {
	contacts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/contacts/+4911100000" {
			w.WriteHeader(404)
			return
		}
		w.Write([]byte(`[{"number":"+4917012345678","uuid":"u-anna","name":"","profile_name":"Anna","profile":{"given_name":"Anna","lastname":"Muster"}},
			{"number":"+4917000000001","uuid":"u-ben1","profile_name":"Ben"},{"number":"+4917000000002","uuid":"u-ben2","name":"Ben"}]`))
	}))
	defer contacts.Close()
	c := joshua()
	c.ByName = map[string]*state.Persona{"hal": hal()}
	c.ByName["hal"].Files = map[string]string{"style.md": "Calm, polite, a little too calm.\n\nMore lines.\n"}
	c.Chat = map[string]string{"+4917012345678": "hal", "+4917000000001": "hal"}
	c.Resolve = SignalContacts(contacts.URL, "+4911100000", nil)
	j := New(func() []string { return []string{"laib"} }, c)
	if out := j.Talk("Anna", "en", "menu"); !strings.HasSuffix(out, "HAL>") {
		t.Fatalf("Anna's profile name resolves to her number and its persona:\n%s", out)
	}
	if out := j.Talk("Anna", "en", "what's on?"); out != "WEITER: -\nPROMPT: HAL>\nFIGUR: HAL — Calm, polite, a little too calm." {
		t.Fatalf("a chat persona's request names the figure for the model:\n%s", out)
	}
	if out := j.Talk("Ben", "en", "menu"); !strings.HasSuffix(out, "JOSHUA>") {
		t.Fatalf("an ambiguous name gets the house persona:\n%s", out)
	}
	if out := j.Talk("Ben", "en", "what's on?"); strings.Contains(out, "FIGUR") {
		t.Fatalf("the house persona needs no FIGUR line:\n%s", out)
	}
	if out := j.Talk("+4917000000001", "en", "menu"); !strings.HasSuffix(out, "HAL>") {
		t.Fatalf("a number still works as is:\n%s", out)
	}
}
