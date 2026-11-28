package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
)

// writeOpen puts an open proposal into the inbox, as the Operator or laib files one.
func writeOpen(t *testing.T, env *cliEnv, id int, more string) {
	t.Helper()
	os.MkdirAll(cli.InboxDir(env), 0o755)
	at := time.Now().UTC().Format(time.RFC3339)
	body := fmt.Sprintf(`{"id":%d,"actor":"ops","branch":"proposal/%d","tier":"apply","status":"open","created":%q,"reason":"Vorschlag %d","files":["services/whoami/values.yml"],%s}`, id, id, at, id, more)
	if err := os.WriteFile(fmt.Sprintf("%s/%d.json", cli.InboxDir(env), id), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const (
	gering = `"class":"values","kind":"values","changes":[{"op":"~","service":"whoami","key":"MODEL","old":"a","new":"b"}]`
	hoch   = `"class":"new-service","kind":"new-service","adds":["mosquitto"],"files":["services/mosquitto/values.yml"]`
)

// ADR 0037 §7 (owner, 2026-11-28: „viel Text, Reihenfolge unklar, viel
// Scrollen, obwohl man meist eh nur freigibt. Das ist gefährlich“): a card
// is its headline, one line of what it does, its weight and whether it
// comes back, the buttons; the rest under Details. Ablauf L-77
func TestACardIsCompactTheRestUnderDetails(t *testing.T) {
	env, _ := testBox(t)
	writeOpen(t, env, 1, gering)
	c := cardOf(inboxMux(env)("GET", "/inbox", nil), 1)
	details := strings.Index(c, "<details><summary>Details</summary>")
	for _, want := range []string{"<h2>#1 · ", `<span class="risk gering">niedrig</span> · rückgängig: ja`, "/inbox/1/approve", "Ablehnen"} {
		if i := strings.Index(c, want); i < 0 || details >= 0 && i > details {
			t.Errorf("above the details the card lacks %q:\n%s", want, c)
		}
	}
	for _, want := range []string{"Was sich ändert", "Wer will das und warum", "Technische Details"} {
		if i := strings.Index(c, want); i < details {
			t.Errorf("%q is not under Details:\n%s", want, c)
		}
	}
}

// ADR 0037 §7: a „hoch“ card's Freigeben shows only with its details open,
// and says what it does. Ablauf L-78
func TestAHighCardsApproveIsInItsDetailsAndSaysWhatItDoes(t *testing.T) {
	env, _ := testBox(t)
	writeOpen(t, env, 2, hoch)
	c := cardOf(inboxMux(env)("GET", "/inbox", nil), 2)
	details, approve := strings.Index(c, "<details><summary>Details</summary>"), strings.Index(c, "/inbox/2/approve")
	if details < 0 || approve < details || !strings.Contains(c[approve:], ">Neuen Dienst freigeben</button>") || !strings.Contains(c, `<span class="risk hoch">hoch</span>`) {
		t.Fatalf("the approve button of a hoch card is not inside its details with its words:\n%s", c)
	}
	if !strings.Contains(c[:details], "Freigeben unter Details") || !strings.Contains(c[:details], "Ablehnen") {
		t.Fatalf("the button row does not say where Freigeben is:\n%s", c)
	}
	writeOpen(t, env, 3, gering) // „Alle freigeben“ leaves it for its own button
	if all := inboxMux(env)("GET", "/inbox/alle", nil); !strings.Contains(all, "einzeln frei") || !strings.Contains(all, "ist „hoch“") || strings.Contains(all, `name=id value=2>`) {
		t.Fatalf("„Alle freigeben“ takes a hoch card along:\n%s", all)
	}
}

// ADR 0037 §7: hoch first; cards that wait on another stand right behind
// it as steps under one line, whatever their numbers. Ablauf L-79
func TestHighFirstAndStepsTogether(t *testing.T) {
	env, _ := testBox(t)
	writeOpen(t, env, 1, gering)
	writeOpen(t, env, 2, hoch)
	writeOpen(t, env, 3, gering+`,"wartetAuf":["vorschlag:2"]`)
	page := inboxMux(env)("GET", "/inbox", nil)
	i1, i2, i3 := strings.Index(page, "id=1 "), strings.Index(page, "id=2 "), strings.Index(page, "id=3 ")
	if !(i2 < i3 && i3 < i1) {
		t.Fatalf("want #2 (hoch), then #3 (its step), then #1: %d %d %d", i2, i3, i1)
	}
	if h := strings.Index(page, "<p class=schritte>Mosquitto vor Whoami – 2 Schritte</p>"); h < 0 || h > i2 {
		t.Fatalf("the steps' line is not above the first step:\n%s", page)
	}
}
