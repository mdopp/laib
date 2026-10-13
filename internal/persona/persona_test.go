package persona

import (
	"strings"
	"testing"
	"time"
)

func term() *Terminal {
	t := New(func() []string { return []string{"laib", "frage"} })
	return t
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
	j := New(func() []string { return []string{"laib", "frage", "haus", "automat"} })
	out := j.Talk("anna", "de", "Was kannst du?")
	for _, want := range []string{"WOPR HAUPTVERZEICHNIS", "1  BOX-STATUS UND VORSCHLÄGE", "3  LICHT, SCHALTER, MUSIK", "5  SPIELE"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing:\n%s", want, out)
		}
	}
	if !strings.HasSuffix(out, "JOSHUA>") {
		t.Fatalf("the prompt is the last line:\n%s", out)
	}
	if out := j.Talk("anna", "de", "3"); !strings.Contains(out, "VERBINDE MIT LICHT, SCHALTER, MUSIK") || !strings.HasSuffix(out, "JOSHUA/HAUS>") {
		t.Fatalf("entering a context:\n%s", out)
	}
	if out := j.Talk("anna", "de", "Mach das Licht im Wohnzimmer aus"); out != "WEITER: haus\nPROMPT: JOSHUA/HAUS>" {
		t.Fatalf("a request in the context goes back to the Bote:\n%s", out)
	}
	if out := j.Talk("anna", "de", "und was läuft gerade?"); out != "WEITER: haus\nPROMPT: JOSHUA/HAUS>" {
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
	if out := j.Talk("p", "en", "2"); !strings.Contains(out, "CONNECTING TO WEB SEARCH WITH SOURCES") || !strings.HasSuffix(out, "JOSHUA/FRAGE>") {
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
