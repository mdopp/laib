package notify

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #703: what a template could not get to the owner (the failed-login counter
// while the Bote is away) lies in Home/eingang, one JSON file per message.
// laib-notify tells it as it tells its own: the Bote first, else one mail;
// the file goes once it is told, or after a day.
func handOver(t *testing.T, c Config, name, betreff, text string, at time.Time) string {
	t.Helper()
	d := filepath.Join(c.Home, "eingang")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{"an": "", "text": text, "betreff": betreff, "at": at.Unix()})
	p := filepath.Join(d, name)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAHandedOverMessageIsMailedOnceWithoutTheBote(t *testing.T) {
	f := newFakeLaib(t)
	addr, mails := fakeSMTP(t)
	c := f.config(t, addr)
	now := time.Now()
	p := handOver(t, c, "anmeldungen-1-1-1.json", "Anmeldung gesperrt: anna (18:10)", "🔒 Anmeldung gesperrt: 6 Fehlversuche für „anna“", now.Add(-20*time.Minute))
	half := filepath.Join(c.Home, "eingang", "anmeldungen-2-1-2.json.neu") // still being written: never read
	os.WriteFile(half, []byte(`{"text": "hal`), 0o644)
	if err := Sweep(context.Background(), c, now); err != nil {
		t.Fatal(err)
	}
	got := mails()
	n := 0
	for _, m := range got {
		if strings.Contains(m, "Anmeldung gesperrt") {
			n++
			if !strings.Contains(m, "6 Fehlversuche f") || !strings.Contains(m, "To: admin@example.home") {
				t.Errorf("the mail: %q", m)
			}
		}
	}
	if n != 1 {
		t.Fatalf("one mail for the handed-over message, got %d: %q", n, got)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("a told message stays in eingang")
	}
	if _, err := os.Stat(half); err != nil {
		t.Error("a file still being written was touched")
	}
	Sweep(context.Background(), c, now.Add(5*time.Minute))
	if len(mails()) != len(got) {
		t.Errorf("mailed twice: %q", mails())
	}
}

func TestAHandedOverMessageTriesTheBoteFirst(t *testing.T) {
	f := newFakeLaib(t)
	b := newFakeBote(t)
	addr, mails := fakeSMTP(t)
	c := chatConfig(t, f, b, addr)
	p := handOver(t, c, "anmeldungen-1-1-1.json", "Vaultwarden: Fehlversuche von 192.0.2.60 (18:10)", "⚠️ Vaultwarden (Passwörter): 10 Fehlversuche", time.Now())
	if err := Sweep(context.Background(), c, time.Now()); err != nil {
		t.Fatal(err)
	}
	m := b.messages()
	if len(m) != 1 || !strings.Contains(m[0]["text"], "10 Fehlversuche") || m[0]["an"] != "martin" {
		t.Fatalf("the Bote is back: the message reaches the chat: %v", m)
	}
	for _, mail := range mails() {
		if strings.Contains(mail, "Vaultwarden") {
			t.Errorf("mailed although the chat took it: %q", mail)
		}
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("a told message stays in eingang")
	}
}

func TestADayOldHandedOverMessageIsDropped(t *testing.T) {
	f := newFakeLaib(t)
	addr, mails := fakeSMTP(t)
	c := f.config(t, addr)
	p := handOver(t, c, "anmeldungen-1-1-1.json", "alt", "alt und vorbei", time.Now().Add(-25*time.Hour))
	bad := handOver(t, c, "kaputt.json", "", "", time.Now())
	os.WriteFile(bad, []byte("{kaputt"), 0o644)
	Sweep(context.Background(), c, time.Now())
	for _, m := range mails() {
		if strings.Contains(m, "alt und vorbei") {
			t.Errorf("a day-old message was mailed: %q", m)
		}
	}
	for _, x := range []string{p, bad} {
		if _, err := os.Stat(x); !os.IsNotExist(err) {
			t.Errorf("%s stays", filepath.Base(x))
		}
	}
}
