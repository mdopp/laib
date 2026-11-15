package haushalt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// kern records what a verb asks of its kernel part and answers as given (#1234).
type kern struct {
	proposals
	asked   [][]string
	auftrag []string
	stdin   []string
	reply   string
	refuse  string
}

func (k *kern) Kern(_ context.Context, auftrag, tool, stdin string, out any, args ...string) error {
	k.asked, k.auftrag, k.stdin = append(k.asked, append([]string{tool}, args...)), append(k.auftrag, auftrag), append(k.stdin, stdin)
	if k.refuse != "" {
		return errors.New("laib: " + k.refuse)
	}
	return json.Unmarshal([]byte(k.reply), out)
}

func run1234(t *testing.T, k *kern, stdin string, args ...string) (string, int, string) {
	t.Helper()
	var out, errb bytes.Buffer
	env := &Env{Call: Call{JSON: true, Actor: "homes", Ceiling: "propose", Auftrag: "A", TZ: "Europe/Berlin", Stdin: stdin}, Laib: k, Out: &out, Err: &errb}
	code := Run(context.Background(), env, args)
	return out.String(), code, errb.String()
}

// #878, #1234: a way is a Homes page, the conversation or an owner's key —
// never an address elsewhere; who it is for is admins, alle or a person.
func TestWegAndFuerAreCheckedBeforeTheKernel(t *testing.T) {
	if w, err := parseWeg("/stimme"); err != nil || w.Seite != "/stimme" || w.Gespraech {
		t.Fatalf("a page: %+v %v", w, err)
	}
	if w, err := parseWeg("gespraech"); err != nil || w.Seite != "" || !w.Gespraech {
		t.Fatalf("the conversation: %+v %v", w, err)
	}
	if w, err := parseWeg("schluessel:OPENAI_KEY"); err != nil || w.Schluessel != "OPENAI_KEY" {
		t.Fatalf("a key: %+v %v", w, err)
	}
	for _, bad := range []string{"", "stimme", "https://evil.example/", "//evil.example", "/a b", "/x?y=<b>", "Gespräch", "schluessel:klein"} {
		if _, err := parseWeg(bad); err == nil {
			t.Errorf("%q was taken for a weg", bad)
		}
	}
	for _, bad := range []string{"Admins", "person:", "person:Anna", "anna", "familie"} {
		if checkFuer(bad) == nil {
			t.Errorf("%q was taken for a fuer", bad)
		}
	}
	for _, args := range [][]string{
		{"aufgabe", "add", "X", "--weg", "https://evil.example/"},
		{"aufgabe", "add", "X", "--fuer", "familie"},
		{"aufgabe", "add", "X", "--erinnern", "morgen"},
		{"aufgabe", "add", "X", "--fertig-wenn", "Raid!"},
		{"aufgabe", "add", "zwei\nZeilen"},
		{"aufgabe", "befund", "3", "Raid!"},
	} {
		k := &kern{reply: `{}`}
		if _, code, _ := run1234(t, k, "", args...); code == exitOK || len(k.asked) != 0 {
			t.Errorf("%q: exit %d, the kernel was asked %v", args, code, k.asked)
		}
	}
}

// #1234: the task goes to the kernel's part as JSON on stdin, with the
// person the caller acts for; the answer is worded here, from what the
// kernel says about who checks it off.
func TestAnAddGoesToTheKernelAsJSONAndIsWordedHere(t *testing.T) {
	k := &kern{reply: `{"aufgabe":{"id":4,"titel":"Schlüssel","fuer":"admins","fertig_wenn":"schluessel OPENAI_KEY"},"selbst":true,"link":"https://laib.box/schluessel#OPENAI_KEY"}`}
	out, code, errs := run1234(t, k, "", "aufgabe", "add", " Schlüssel ", "--weg", "schluessel:OPENAI_KEY", "--von", "lena")
	if code != exitOK || len(k.asked) != 1 || strings.Join(k.asked[0], " ") != "laib_aufgabe_kern add --von lena" || k.auftrag[0] != "A" {
		t.Fatalf("exit %d %s: %v", code, errs, k.asked)
	}
	var sent Aufgabe
	if err := json.Unmarshal([]byte(k.stdin[0]), &sent); err != nil || sent.Titel != "Schlüssel" || sent.Fuer != "admins" || sent.Weg == nil || sent.Weg.Schluessel != "OPENAI_KEY" {
		t.Fatalf("stdin %q: %v", k.stdin[0], err)
	}
	var got struct{ Message string }
	if json.Unmarshal([]byte(out), &got); got.Message != "Box-Aufgabe #4 angelegt: Schlüssel. Laib hakt sie selbst ab, sobald der Schlüssel eingetragen ist: https://laib.box/schluessel#OPENAI_KEY" {
		t.Fatalf("message %q", got.Message)
	}
	// the kernel's no is the verb's, refused, in its words
	k = &kern{refuse: "Box-Aufgabe #9 gibt es nicht"}
	if _, code, errs := run1234(t, k, "", "aufgabe", "erledigt", "9"); code != exitRefused || errs != "laib: Box-Aufgabe #9 gibt es nicht\n" {
		t.Fatalf("exit %d %q", code, errs)
	}
}

// #1234: list counts and filters what the kernel let the caller see.
func TestAListIsCountedAndFilteredHere(t *testing.T) {
	k := &kern{reply: `{"aufgaben":[{"id":1,"titel":"a","fuer":"admins"},{"id":2,"titel":"b","fuer":"admins","erinnern":"2000-01-01"},{"id":3,"titel":"c","fuer":"admins","erledigt":{"am":"2026-01-01","von":"x"}}],"schluessel":[]}`}
	out, code, _ := run1234(t, k, "", "aufgabe", "list", "--faellig")
	var got struct {
		Offen, Faellig int
		Aufgaben       []Aufgabe
		Schluessel     []any
	}
	if json.Unmarshal([]byte(out), &got); code != exitOK || got.Offen != 2 || got.Faellig != 1 || len(got.Aufgaben) != 1 || got.Aufgaben[0].ID != 2 || got.Schluessel == nil {
		t.Fatalf("exit %d: %s", code, out)
	}
}
