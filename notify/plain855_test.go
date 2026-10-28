package notify

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"
)

// #855: the owner's red mail read „Get "http://…": dial tcp …: connection
// refused → laib logs paperless … laib diag zeigt alles.“ What a person
// reads first is the finding's plain sentence and the next step; the raw
// lines follow under Details for whoever looks after the box.
var technical = regexp.MustCompile(`\blaib [a-z]|\b[\w@-]+\.(service|timer)\b|dial tcp|connection refused|\b(Get|Post) "`)

func TestTheRedMailSpeaksPlainlyAndKeepsTheDetails(t *testing.T) {
	f := newFakeLaib(t)
	addr, mails := fakeSMTP(t)
	c := f.config(t, addr)
	f.diag = `[{"severity":"rot","probe":"health","service":"paperless","detail":"Get \"http://127.0.0.1:8000/\": dial tcp 127.0.0.1:8000: connect: connection refused","fix":"laib logs paperless","say":"Paperless antwortet nicht."}]`
	now := time.Now()
	for _, at := range []time.Time{now, now.Add(11 * time.Minute)} {
		if err := Sweep(context.Background(), c, at); err != nil {
			t.Fatal(err)
		}
	}
	var mail string
	for _, m := range mails() {
		if strings.Contains(m, "seit 10 Minuten rot") {
			mail = m
		}
	}
	if mail == "" {
		t.Fatalf("no red mail: %q", mails())
	}
	top, details, _ := strings.Cut(mail, "Details für den, der sich darum kümmert:")
	if m := technical.FindString(top); m != "" {
		t.Errorf("the red mail shows %q before its Details:\n%s", m, top)
	}
	if !strings.Contains(top, "Paperless antwortet nicht.") || !strings.Contains(top, "Was du tun kannst") {
		t.Errorf("the red mail lacks the plain sentence or the next step:\n%s", top)
	}
	if !strings.Contains(details, "dial tcp") || !strings.Contains(details, "laib logs paperless") {
		t.Errorf("the raw lines are gone, not moved under Details:\n%s", details)
	}
}
