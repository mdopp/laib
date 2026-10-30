package notify

import (
	"bufio"
	"context"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// gmailSMTP is as strict as Gmail where notify's mail failed (#659): a MAIL
// FROM or RCPT TO that is not one bare <addr-spec> is 555 5.5.2. While
// refuse is set it answers every MAIL so; it counts the MAILs it was sent.
type gmailSMTP struct {
	mu     sync.Mutex
	refuse bool
	tries  int
	mails  []string
	addr   string
}

var bare = regexp.MustCompile(`^(MAIL FROM|RCPT TO):<[^<>\s"]+@[^<>\s"]+>( [A-Z0-9=]+)*$`)

func newGmailSMTP(t *testing.T) *gmailSMTP {
	g := &gmailSMTP{}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	g.addr = l.Addr().String()
	syntax := "555-5.5.2 Syntax error, cannot decode response. For more information, go to\r\n555-5.5.2  https://support.google.com/a/answer/3221692 and review RFC 5321\r\n555 5.5.2 specifications. x-test - gsmtp\r\n"
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				fmt.Fprint(c, "220 mx.example ESMTP\r\n")
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					cmd := strings.TrimRight(line, "\r\n")
					up := strings.ToUpper(cmd)
					switch {
					case strings.HasPrefix(up, "EHLO"), strings.HasPrefix(up, "HELO"):
						fmt.Fprint(c, "250-mx.example\r\n250 8BITMIME\r\n")
					case strings.HasPrefix(up, "MAIL"), strings.HasPrefix(up, "RCPT"):
						g.mu.Lock()
						refuse := g.refuse
						if strings.HasPrefix(up, "MAIL") {
							g.tries++
						}
						g.mu.Unlock()
						if refuse || !bare.MatchString(cmd) {
							fmt.Fprint(c, syntax)
							return
						}
						fmt.Fprint(c, "250 ok\r\n")
					case up == "DATA":
						fmt.Fprint(c, "354 go\r\n")
						var data strings.Builder
						for {
							l, err := r.ReadString('\n')
							if err != nil || l == ".\r\n" {
								break
							}
							if !strings.HasSuffix(l, "\r\n") || strings.Contains(strings.TrimSuffix(l, "\r\n"), "\r") {
								fmt.Fprint(c, "554 bare CR or LF\r\n")
								return
							}
							data.WriteString(l)
						}
						g.mu.Lock()
						g.mails = append(g.mails, data.String())
						g.mu.Unlock()
						fmt.Fprint(c, "250 ok\r\n")
					case up == "QUIT":
						fmt.Fprint(c, "221 bye\r\n")
						return
					default:
						fmt.Fprint(c, "250 ok\r\n")
					}
				}
			}(c)
		}
	}()
	return g
}

func (g *gmailSMTP) got() (int, []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.tries, append([]string(nil), g.mails...)
}

// #659: SMTP_FROM is Authelia's sender, "Name <addr>"; the envelope takes the
// bare address, the header the name, RFC 2047-encoded where it is not ASCII.
func TestTheMailGoesOutWithAutheliasSenderForm(t *testing.T) {
	f := newFakeLaib(t)
	g := newGmailSMTP(t)
	c := f.config(t, g.addr)
	c.SMTPFrom = "Die Bäckerbox <box@example.home>"
	f.updates = `[{"service":"whoami","image":"docker.io/traefik/whoami","current":"v1.11","minor":"v1.12"}]`
	if err := Sweep(context.Background(), c, time.Now()); err != nil {
		t.Fatalf("Gmail refuses the mail: %v", err)
	}
	_, mails := g.got()
	if len(mails) != 1 {
		t.Fatalf("one mail: %q", mails)
	}
	m, err := mail.ReadMessage(strings.NewReader(mails[0]))
	if err != nil {
		t.Fatal(err)
	}
	from, err := m.Header.AddressList("From")
	if err != nil || len(from) != 1 || from[0].Address != "box@example.home" || from[0].Name != "Die Bäckerbox" {
		t.Fatalf("the From header: %q %v", m.Header.Get("From"), err)
	}
	if subj, err := new(mime.WordDecoder).DecodeHeader(m.Header.Get("Subject")); err != nil || subj != "Laib: Was ist veraltet (e2e.home)" {
		t.Fatalf("the subject: %q %v", subj, err)
	}
	if m.Header.Get("MIME-Version") != "1.0" {
		t.Errorf("no MIME-Version: %q", mails[0])
	}
}

// #659, the box on 2026-10-24: #101 and #102 carry about a hundred impact
// lines, far over bote-notify's 4096 bytes; it answered 413, notify fell back
// to a mail that failed and stopped at the first error, and laib_inbox lists
// the newest first: nothing older reached the chat either.
func TestAProposalWithAHundredImpactsReachesTheChatAndBlocksNoOther(t *testing.T) {
	f := newFakeLaib(t)
	b := newFakeBote(t)
	c := chatConfig(t, f, b, "127.0.0.1:1") // the mail fails, as Gmail's did
	var impact []string
	for i := 0; i < 100; i++ {
		impact = append(impact, fmt.Sprintf(`"Achtung: ändert x%d=E.get(\"X\") <&> in auth/anmeldungen.py, was Laib nicht einschätzen kann"`, i))
	}
	f.inbox = `[{"id":102,"reason":"template update auth","tier":"apply","class":"human","created":"2026-10-25T03:52:09Z","card":{"summary":"Auth: 5 Einstellungen ändern sich","impact":[` + strings.Join(impact, ",") + `],"reversible":"laib rollback"}},
	            {"id":99,"reason":"template update media","tier":"apply","class":"template-update","created":"2026-10-24T16:35:05Z","card":{"summary":"Media: SPOKEN_WORD ändert sich"}}]`
	Sweep(context.Background(), c, time.Now())
	got := b.messages()
	if len(got) != 2 {
		t.Fatalf("both proposals reach the chat: %v", got)
	}
	s := got[0]["text"]
	if s != "Freigabe #102: Auth: 5 Einstellungen ändern sich · https://laib.e2e.home/inbox#102" { // #778, its card #857: the impacts are the Inbox's
		t.Errorf("the long one is one line pointing to the Inbox: %q", s)
	}
	if !strings.Contains(got[1]["text"], "Freigabe #99") {
		t.Errorf("the older one too: %v", got[1])
	}
}

// #659: a failed chat is tried again on every run while the proposal is open;
// the mail is the fallback, sent once; once the Bote takes it again the
// proposal reaches the chat, once.
func TestAProposalReachesTheChatOnceTheBoteIsBack(t *testing.T) {
	f := newFakeLaib(t)
	b := newFakeBote(t)
	g := newGmailSMTP(t)
	c := chatConfig(t, f, b, g.addr)
	f.inbox = `[{"id":100,"reason":"template update music-assistant","tier":"apply","class":"human","created":"2026-10-24T16:35:05Z","card":{"summary":"Music Assistant: ABS_TOKEN wird ein Geheimnis"}}]`
	b.down = true
	now := time.Now()
	Sweep(context.Background(), c, now)
	Sweep(context.Background(), c, now.Add(5*time.Minute))
	if _, mails := g.got(); len(mails) != 1 || !strings.Contains(mails[0], "Freigabe #100") {
		t.Fatalf("the chat down: one mail, not one per run: %q", mails)
	}
	b.down = false
	if err := Sweep(context.Background(), c, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	Sweep(context.Background(), c, now.Add(15*time.Minute))
	if got := b.messages(); len(got) != 1 || !strings.Contains(got[0]["text"], "Freigabe #100") {
		t.Fatalf("the Bote back: the proposal reaches the chat, once: %v", got)
	}
	if _, mails := g.got(); len(mails) != 1 {
		t.Fatalf("no second mail: %d", len(mails))
	}
}

// #659: a failed chat send is journaled with its reason, each distinct
// failure once an hour, not every five minutes; a message that still fails
// after GiveUp is not mailed any more, with a last note.
func TestFailuresAreJournaledOnceAnHourAndTheMailGivesUp(t *testing.T) {
	f := newFakeLaib(t)
	b := newFakeBote(t)
	g := newGmailSMTP(t)
	g.refuse = true
	c := chatConfig(t, f, b, g.addr)
	b.down = true
	f.inbox = `[{"id":103,"reason":"template update audiobookshelf","tier":"apply","class":"human","created":"2026-10-25T04:15:43Z","card":{"summary":"Audiobookshelf: abs-pflege.py ändert sich"}}]`
	now := time.Now()
	Sweep(context.Background(), c, now)
	notes := append([]string(nil), f.notes...)
	var chat, mailed bool
	for _, n := range notes {
		chat = chat || strings.HasPrefix(n, "watch mitteilung an martin: ") && strings.Contains(n, "502")
		mailed = mailed || strings.HasPrefix(n, "watch mail an admin@example.home: ") && strings.Contains(n, "555")
	}
	if !chat || !mailed || len(notes) != 2 {
		t.Fatalf("one note for the chat with its reason, one for the mail: %q", notes)
	}
	for m := 5; m < 60; m += 5 {
		Sweep(context.Background(), c, now.Add(time.Duration(m)*time.Minute))
	}
	if len(f.notes) != 2 {
		t.Fatalf("the same failures within the hour are not noted again: %q", f.notes[2:])
	}
	Sweep(context.Background(), c, now.Add(61*time.Minute))
	if len(f.notes) != 4 {
		t.Fatalf("an hour later each is noted again: %q", f.notes[2:])
	}
	tries, _ := g.got()
	Sweep(context.Background(), c, now.Add(GiveUp+time.Minute))
	Sweep(context.Background(), c, now.Add(GiveUp+6*time.Minute))
	if n, _ := g.got(); n != tries {
		t.Errorf("after %s no more mail: %d tries, %d before", GiveUp, n, tries)
	}
	last := 0
	for _, n := range f.notes {
		if strings.Contains(n, "keine weitere Mail") {
			last++
		}
	}
	if last != 1 {
		t.Fatalf("one last note: %q", f.notes)
	}
	b.down = false
	Sweep(context.Background(), c, now.Add(GiveUp+11*time.Minute))
	if got := b.messages(); len(got) != 1 {
		t.Fatalf("the chat is still tried: %v", got)
	}
}
