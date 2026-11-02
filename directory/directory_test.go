package directory

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Input is Talk in the session's language (English for a new one).
func (t *Terminal) Input(sid, line string) string { return t.Talk(sid, "", line) }

func homes() Who { return Who{Name: "Homes"} }

// term is Homes with the box and the web search; "p" is the admin's
// session, so its directory has the box level (#863).
func term() *Terminal {
	c := homes()
	c.Admins = []string{"p"}
	return New(func() []string { return []string{"laib", "frage"} }, c)
}

// asAdmin is Homes with sid as the admin's (#863).
func asAdmin(sid string) Who {
	c := homes()
	c.Admins = []string{sid}
	return c
}

// ADR 0030: the name is box.yml's, the directory has no other costume.
func TestTheNameIsTheAssistants(t *testing.T) {
	j := New(func() []string { return []string{"frage"} }, Who{Name: "Jarvis"})
	if out := j.Talk("p", "de", "Menü"); !strings.HasSuffix(out, "JARVIS>") {
		t.Fatalf("the prompt carries the name:\n%s", out)
	}
	if out := j.Talk("p", "de", "Jarvis"); !strings.HasPrefix(out, "TEXT:\nHallo, hier ist Jarvis.") {
		t.Fatalf("the name alone is a hello:\n%s", out)
	}
	if out := New(nil, Who{}).Talk("p", "de", "Menü"); !strings.HasSuffix(out, "HOMES>") {
		t.Fatalf("no name is Homes:\n%s", out)
	}
}

func TestLogoffEndsTheSessionAndAnIdleOneExpires(t *testing.T) {
	j := term()
	j.Input("p", "Menu")
	if out := j.Input("p", "logoff"); !strings.Contains(out, "GOODBYE") {
		t.Fatalf("logoff:\n%s", out)
	}
	if _, ok := j.sessions["p"]; ok {
		t.Fatal("the session outlived LOGOFF")
	}
	now := time.Now()
	j.now = func() time.Time { return now }
	j.Input("q", "1")
	j.Input("q", "1")
	j.now = func() time.Time { return now.Add(31 * time.Minute) }
	if out := j.Input("q", "what's up?"); out != "WEITER: -\nPROMPT: HOMES>" {
		t.Fatalf("an idle session after 30 min starts over at the top:\n%s", out)
	}
}

func TestTheToolPassesTextThrough(t *testing.T) {
	srv := Server(term(), "test")
	if len(srv.Tools) != 1 || srv.Tools[0].Name != "directory" {
		t.Fatalf("one tool, directory: %+v", srv.Tools)
	}
	out, err := srv.Tools[0].Call(nil, map[string]any{"eingabe": "Menu", "sitzung": "Anna", "sprache": "en"})
	if err != nil || !strings.Contains(out, "DIRECTORY") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// #122: the directory is the Bote's front. You are always somewhere, the prompt
// shows where, and you stay there until ZURÜCK or MENÜ. Where you are is
// this code; a request inside a context goes back to the Bote as WEITER.
func TestLevelsWithContext(t *testing.T) {
	j := New(func() []string { return []string{"laib", "frage", "ha", "automat"} }, asAdmin("anna"))
	out := j.Talk("anna", "de", "Menü")
	for _, want := range []string{"VERZEICHNIS", "1  BOX-STATUS UND VORSCHLÄGE", "3  LICHT, SCHALTER, MUSIK", "(GERÄTE)"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing:\n%s", want, out)
		}
	}
	if !strings.HasSuffix(out, "HOMES>") {
		t.Fatalf("the prompt is the last line:\n%s", out)
	}
	if strings.Contains(out, "(HA)") || strings.Contains(out, "(LAIB)") {
		t.Fatalf("the family sees words, not tool names (ADR 0021):\n%s", out)
	}
	if out := j.Talk("anna", "de", "3"); !strings.Contains(out, "VERBINDE MIT LICHT, SCHALTER, MUSIK") || !strings.HasSuffix(out, "HOMES/GERÄTE>") {
		t.Fatalf("entering a context:\n%s", out)
	}
	if out := j.Talk("anna", "de", "Mach das Licht im Wohnzimmer aus"); out != "WEITER: ha\nPROMPT: HOMES/GERÄTE>" {
		t.Fatalf("a request in the context goes back to the Bote:\n%s", out)
	}
	if out := j.Talk("anna", "de", "und was läuft gerade?"); out != "WEITER: ha\nPROMPT: HOMES/GERÄTE>" {
		t.Fatalf("the context stays:\n%s", out)
	}
	if out := j.Talk("anna", "de", "zurück"); !strings.Contains(out, "VERZEICHNIS") || !strings.HasSuffix(out, "HOMES>") {
		t.Fatalf("ZURÜCK goes up one level:\n%s", out)
	}
	if out := j.Talk("anna", "de", "Wie wird das Wetter morgen?"); out != "WEITER: -\nPROMPT: HOMES>" {
		t.Fatalf("a free question at the top goes to the Bote with any tool:\n%s", out)
	}
	if out := j.Talk("ben", "de", "Wie spät ist es?"); out != "WEITER: -\nPROMPT: HOMES>" {
		t.Fatalf("a first message that is a question is answered, not a menu:\n%s", out)
	}
}

func TestTheDirectorySpeaksThePersonsLanguage(t *testing.T) {
	j := term()
	out := j.Talk("p", "en", "Menu")
	for _, want := range []string{"DIRECTORY", "1  BOX STATUS AND PROPOSALS", "HOMES>"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing:\n%s", want, out)
		}
	}
	if out := j.Talk("p", "en", "2"); !strings.Contains(out, "CONNECTING TO WEB SEARCH WITH SOURCES") || !strings.HasSuffix(out, "HOMES/SEARCH>") {
		t.Fatalf("english context:\n%s", out)
	}
}

func TestTheToolTakesTheLanguageAndDefaultsToGerman(t *testing.T) {
	srv := Server(term(), "test")
	out, _ := srv.Tools[0].Call(nil, map[string]any{"eingabe": "Menü", "sitzung": "Anna"})
	if !strings.Contains(out, "VERZEICHNIS") {
		t.Fatalf("german by default:\n%s", out)
	}
	out, _ = srv.Tools[0].Call(nil, map[string]any{"eingabe": "Menu", "sitzung": "Bob", "sprache": "en"})
	if !strings.Contains(out, "DIRECTORY") {
		t.Fatalf("english when asked:\n%s", out)
	}
}

// #587: the owner answered Hermes' drop notice ("… damit unbekannte Absender …") with "Dir
// sollte die Nummer bekannt sein"; the quote's ABSENDER held ENDE, and the Bote said AUF
// WIEDERSEHEN. A sentence is a request for the Bote whatever word it holds; only a short
// message that is the command or the greeting is one.
func TestASentenceIsARequestNotADirectoryLine(t *testing.T) {
	for _, line := range []string{
		`[Replying to: "Laib hat eine Nachricht verworfen, damit unbekannte Absender …"] Dir sollte die Nummer bekannt sein`,
		"Am Wochenende kommt Oma",
		"Hallo, wie hoch ist der höchste Berg?",
		"Homes, schalte das Licht in der Küche an",
		"Ich brauche Hilfe beim Rezept",
		"Kannst du das Licht im Exit-Flur ausmachen?",
	} {
		j := term()
		if out := j.Talk("p", "de", line); !strings.HasPrefix(out, "WEITER: ") {
			t.Errorf("%q got a directory screen, not the Bote:\n%s", line, out)
		}
	}
	for line, want := range map[string]string{"logoff": "AUF WIEDERSEHEN.", "Tschüss Homes!": "AUF WIEDERSEHEN.", "bye": "AUF WIEDERSEHEN.",
		"Hallo": "Hallo, hier ist Homes.", "Hallo Homes": "Hallo, hier ist Homes.", "Was kannst du?": "Hallo, hier ist Homes.",
		"Homes": "Hallo, hier ist Homes.", "Hilfe": "Hallo, hier ist Homes.", "Menü": "VERZEICHNIS"} {
		j := term()
		if out := j.Talk("p", "de", line); !strings.Contains(out, want) {
			t.Errorf("%q: want %q:\n%s", line, want, out)
		}
	}
}

// #127, #863: Hermes shows the model the sender's Signal profile name, not
// the number, so the admin's name resolves through signal-cli's contacts in
// the pod. One match: that number; none or two: family.
func TestANameResolvesToTheAdminsNumber(t *testing.T) {
	contacts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/contacts/+4911100000" {
			w.WriteHeader(404)
			return
		}
		w.Write([]byte(`[{"number":"+4917012345678","uuid":"u-anna","name":"","profile_name":"Anna","profile":{"given_name":"Anna","lastname":"Muster"}},
			{"number":"+4917000000001","uuid":"u-ben1","profile_name":"Ben"},{"number":"+4917000000002","uuid":"u-ben2","name":"Ben"}]`))
	}))
	defer contacts.Close()
	c := homes()
	c.Admins = []string{"+4917012345678", "+4917000000001"}
	c.Resolve = SignalContacts(contacts.URL, "+4911100000", nil)
	j := New(func() []string { return []string{"laib"} }, c)
	for sid, admin := range map[string]bool{"Anna": true, "Anna Muster": true, "Ben": false, "+4917000000001": true, "Carl": false} {
		if out := j.Talk(sid, "de", "Menü"); strings.Contains(out, "BOX-STATUS") != admin {
			t.Errorf("%s: admin %v:\n%s", sid, admin, out)
		}
	}
}
