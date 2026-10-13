package joshua

import (
	"strings"
	"testing"
	"time"
)

func term() *Terminal {
	t := New(func() []string { return []string{"die Box (laib: lesen und vorschlagen)", "Fragen (frage: Websuche mit Quellen)"} })
	return t
}

// #119: the trigger opens WOPR's prompt: what the system can do, then games.
func TestTheTriggerOpensThePromptWithCapabilitiesAndGames(t *testing.T) {
	for _, in := range []string{"Shall we play a game?", "Wollen wir ein Spiel spielen?", "Joshua"} {
		out := term().Input("p", in)
		for _, want := range []string{"GREETINGS PROFESSOR FALKEN.", "WHAT I CAN DO", "DIE BOX (LAIB: LESEN UND VORSCHLAGEN)", "FRAGEN (FRAGE: WEBSUCHE MIT QUELLEN)", "GAMES", "SHALL WE PLAY A GAME?"} {
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
