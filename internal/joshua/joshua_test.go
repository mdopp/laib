package joshua

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
	if !strings.HasSuffix(strings.TrimSpace(out), "GLOBAL THERMONUCLEAR WAR") {
		t.Fatalf("Global Thermonuclear War comes last:\n%s", out)
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
	if len(srv.Tools) != 1 || srv.Tools[0].Name != "joshua" {
		t.Fatalf("one tool, joshua: %+v", srv.Tools)
	}
	out, err := srv.Tools[0].Call(nil, map[string]any{"eingabe": "Shall we play a game?", "sitzung": "Anna"})
	if err != nil || !strings.Contains(out, "SHALL WE PLAY A GAME?") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// The directory is the Bote's real tools; GAMES is the last entry.
func TestTheDirectoryLeadsToGamesAndHandsAToolToTheBote(t *testing.T) {
	j := New(func() []string { return []string{"laib", "frage", "haus", "kalender-x"} })
	out := j.Input("p", "Joshua")
	if !strings.Contains(out, "4  KALENDER-X") || !strings.Contains(out, "5  GAMES") {
		t.Fatalf("an unknown tool keeps its name; GAMES comes last:\n%s", out)
	}
	if out := j.Input("p", "5"); !strings.HasSuffix(strings.TrimSpace(out), "GLOBAL THERMONUCLEAR WAR") {
		t.Fatalf("5 is GAMES:\n%s", out)
	}
	j.Input("p", "Joshua")
	out = j.Input("p", "2")
	if !strings.Contains(out, "CONNECTING TO WEB SEARCH WITH SOURCES") || !strings.HasSuffix(strings.TrimSpace(out), "HANDOVER: frage") {
		t.Fatalf("choosing a tool hands it to the Bote:\n%s", out)
	}
	if _, ok := j.sessions["p"]; ok {
		t.Fatal("Joshua is still running after the handover")
	}
}

// Free wording is the model's job; when Joshua does not understand, it says
// which commands it takes, so the model can map the person's words and retry.
func TestUnrecognisedInputNamesTheValidCommands(t *testing.T) {
	j := term()
	j.Input("p", "Joshua")
	if out := j.Input("p", "was kannst du so?"); !strings.Contains(out, "VALID:") || !strings.Contains(out, "LIST GAMES") || !strings.Contains(out, "LOGOFF") {
		t.Fatalf("the menu's commands:\n%s", out)
	}
	j.Input("p", "play tic-tac-toe")
	j.Input("p", "1")
	if out := j.Input("p", "ich nehme die ecke"); !strings.Contains(out, "VALID:") || !strings.Contains(out, "1-9") {
		t.Fatalf("the game's commands:\n%s", out)
	}
	if out := j.Input("p", "Mitte"); !strings.Contains(out, " X ") {
		t.Fatalf("MITTE is 5:\n%s", out)
	}
}

// A first message in free words opens the directory, not a list it happens
// to contain ("Lass uns was SPIELEn"); "Joshua" mid-game brings it back too.
func TestFreeWordsOpenTheDirectory(t *testing.T) {
	j := term()
	if out := j.Input("p", "Lass uns was spielen"); !strings.Contains(out, "WOPR MAIN DIRECTORY") {
		t.Fatalf("free words first:\n%s", out)
	}
	j.Input("p", "play tic-tac-toe")
	j.Input("p", "1")
	if out := j.Input("p", "Joshua"); !strings.Contains(out, "WOPR MAIN DIRECTORY") {
		t.Fatalf("Joshua mid-game:\n%s", out)
	}
}
