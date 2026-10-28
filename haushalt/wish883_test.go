package haushalt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

// tasks records what a verb hands laib as `laib aufgabe …`.
type tasks struct {
	proposals
	added   [][]string
	ceiling string
	refuse  string
}

func (k *tasks) Aufgabe(_ context.Context, ceiling string, args ...string) (int, error) {
	if k.refuse != "" {
		return 0, errors.New(k.refuse)
	}
	k.added, k.ceiling = append(k.added, args), ceiling
	return 7, nil
}

func run883(t *testing.T, actor string, laib Laib, gh GitHub, in *Intake, args ...string) (map[string]any, int, string, string) {
	t.Helper()
	var out, errb, jr bytes.Buffer
	env := &Env{Call: Call{JSON: true, Actor: actor, Ceiling: "propose", Intake: in}, Laib: laib, GitHub: gh, Out: &out, Err: &errb, Journal: &jr}
	code := Run(context.Background(), env, args)
	var reply map[string]any
	json.Unmarshal(out.Bytes(), &reply)
	return reply, code, errb.String(), jr.String()
}

// argAfter is the value after name in a `laib aufgabe` argv.
func argAfter(args []string, name string) string {
	if i := slices.Index(args, name); i >= 0 && i+1 < len(args) {
		return args[i+1]
	}
	return ""
}

// #883 (owner, 2026-10-28): a wish from the Bote's hand becomes a
// Box-Aufgabe for the admins, „Wunsch aus dem Boten: …“, with the person
// who asked, never a GitHub issue: the owner decides what goes on to the
// Werkstatt. It goes back to laib over MCP under the call's ceiling; with
// and without an intake, nothing reaches GitHub.
func TestABotesWishIsABoxAufgabeForTheAdmins(t *testing.T) {
	g := newGitHub(t)
	gh := GitHub{API: g.srv.URL, Token: "ghp_haushalt"}
	for _, in := range []*Intake{{Repo: "mdopp/x", Label: "wunsch", Domain: "nuc.home"}, {}} {
		k := &tasks{}
		got, code, errs, journal := run883(t, "bote", k, gh, in, "wish", "--von", "lena", "Ich hätte gern einen Dienst, der Rezepte sammelt.\n\nGesehen: dreimal gefragt")
		if code != exitOK || got["aufgabe"] != float64(7) || got["issue"] != nil {
			t.Fatalf("exit %d: %v %s", code, got, errs)
		}
		if len(g.calls) != 0 {
			t.Fatalf("the Bote's wish reached GitHub: %v", g.calls)
		}
		if len(k.added) != 1 || k.ceiling != "propose" {
			t.Fatalf("one Box-Aufgabe under the call's ceiling: %v %q", k.added, k.ceiling)
		}
		a := k.added[0]
		if a[0] != "add" || !strings.HasPrefix(a[1], "Wunsch aus dem Boten: Ich hätte gern einen Dienst, der Rezepte sammelt") || strings.ContainsAny(a[1], "\n.") {
			t.Fatalf("title %q", a[1])
		}
		if argAfter(a, "--fuer") != "admins" || argAfter(a, "--von") != "bote:lena" {
			t.Fatalf("for the admins, from the person who asked: %v", a)
		}
		if w := argAfter(a, "--warum"); !strings.Contains(w, "Rezepte sammelt") || !strings.Contains(w, "dreimal gefragt") || !strings.Contains(w, "lena") || strings.ContainsAny(w, "\n\r") {
			t.Fatalf("the person's words on one line, and who asked: %q", w)
		}
		if !strings.Contains(journal, "Box-Aufgabe #7") {
			t.Fatalf("the journal names the task: %q", journal)
		}
		if msg, _ := got["message"].(string); !strings.Contains(msg, "Admin") {
			t.Fatalf("the answer says the admin decides: %q", msg)
		}
	}

	// without von: still the admins' task, named as from the Bote
	k := &tasks{}
	if _, code, errs, _ := run883(t, "bote", k, gh, &Intake{}, "wish", "ein Kalender für alle"); code != exitOK || len(k.added) != 1 || argAfter(k.added[0], "--von") != "bote" {
		t.Fatalf("no von: exit %d %s %v", code, errs, k.added)
	}
	// laib refuses: an error with laib's reason, nothing filed elsewhere
	if _, code, errs, journal := run883(t, "bote", &tasks{refuse: "aufgaben.yml: busy"}, gh, &Intake{Repo: "mdopp/x"}, "wish", "noch einer"); code == exitOK || !strings.Contains(errs, "busy") || !strings.Contains(journal, `"red"`) || len(g.calls) != 0 {
		t.Fatalf("refused: exit %d %q %q", code, errs, journal)
	}
	// no text: usage
	if _, code, _, _ := run883(t, "bote", &tasks{}, gh, &Intake{}, "wish", "--von", "lena", " "); code == exitOK {
		t.Fatal("an empty wish was taken")
	}
}

// #883: wishes from every other hand are unchanged: claude's, Haus's and a
// human's still become an issue in box.yml intake.repo, and nothing is
// handed to laib's Box-Aufgaben.
func TestAWishFromAnotherHandIsStillAnIssue(t *testing.T) {
	for _, actor := range []string{"claude", "haus", "human:core"} {
		g := newGitHub(t)
		k := &tasks{}
		got, code, errs, _ := run883(t, actor, k, GitHub{API: g.srv.URL, Token: "ghp_haushalt"}, &Intake{Repo: "mdopp/x", Label: "wunsch"}, "wish", "ein Kalender für alle")
		if code != exitOK || got["issue"] != "https://github.com/mdopp/x/issues/42" || len(g.calls) != 1 || len(k.added) != 0 {
			t.Fatalf("%s: exit %d %v %s, %d issues, %v", actor, code, got, errs, len(g.calls), k.added)
		}
	}
}
