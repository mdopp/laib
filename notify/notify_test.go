package notify

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP accepts mails without auth (SMTP_SECURITY none) and keeps each message.
func fakeSMTP(t *testing.T) (string, func() []string) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	var mu sync.Mutex
	var mails []string
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				fmt.Fprint(c, "220 fake\r\n")
				var data strings.Builder
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					cmd := strings.ToUpper(strings.TrimSpace(line))
					switch {
					case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
						fmt.Fprint(c, "250 fake\r\n")
					case cmd == "DATA":
						fmt.Fprint(c, "354 go\r\n")
						for {
							l, err := r.ReadString('\n')
							if err != nil || l == ".\r\n" {
								break
							}
							data.WriteString(l)
						}
						mu.Lock()
						mails = append(mails, data.String())
						mu.Unlock()
						data.Reset()
						fmt.Fprint(c, "250 ok\r\n")
					case cmd == "QUIT":
						fmt.Fprint(c, "221 bye\r\n")
						return
					default:
						fmt.Fprint(c, "250 ok\r\n")
					}
				}
			}(c)
		}
	}()
	return l.Addr().String(), func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), mails...) }
}

// fakeLaib is laib serve as the notifier sees it: /ready, and /mcp with the
// notify hand's three tools; it records each call and the bearer.
type fakeLaib struct {
	mu       sync.Mutex
	red      bool   // a red finding for whoami, and /ready red
	updates  string // laib_updates' JSON
	notes    []string
	bearer   []string
	srv      *httptest.Server
	refusals int
}

func newFakeLaib(t *testing.T) *fakeLaib {
	f := &fakeLaib{updates: "[]"}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.URL.Path == "/ready" {
			red, state := 0, "bereit"
			if f.red {
				red = 1
			}
			json.NewEncoder(w).Encode(map[string]any{"state": state, "red": red})
			return
		}
		f.bearer = append(f.bearer, r.Header.Get("Authorization"))
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				Name string         `json:"name"`
				Args map[string]any `json:"arguments"`
			} `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		text := ""
		switch req.Params.Name {
		case "laib_diag":
			text = "[]"
			if f.red {
				text = `[{"severity":"rot","probe":"unit","service":"whoami","detail":"whoami.service ist inactive","fix":"laib logs whoami"},{"severity":"gelb","probe":"watchdog","detail":"x"}]`
			}
		case "laib_updates":
			text = f.updates
		case "laib_note":
			f.notes = append(f.notes, fmt.Sprint(req.Params.Args["kind"], " ", req.Params.Args["text"]))
			text = "notiert"
		default:
			f.refusals++
		}
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"content": []map[string]string{{"type": "text", "text": text}}}})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeLaib) config(t *testing.T, smtpAddr string) Config {
	host, port, _ := net.SplitHostPort(smtpAddr)
	return Config{MCPURL: f.srv.URL + "/mcp", ReadyURL: f.srv.URL + "/ready", Token: "tok-n", Home: t.TempDir(), Domain: "e2e.home",
		MailTo: "admin@example.home", SMTPHost: host, SMTPPort: port, SMTPSecurity: "none", SMTPFrom: "box@example.home"}
}

// #393, moved with the notifier (#443): a service red for ten minutes is one
// mail per incident; once a week the pending image updates; the texts as before.
func TestTheOwnerGetsAMailWhenAServiceIsRedForTenMinutes(t *testing.T) {
	f := newFakeLaib(t)
	addr, mails := fakeSMTP(t)
	c := f.config(t, addr)
	f.updates = `[{"service":"whoami","key":"IMAGE","image":"docker.io/traefik/whoami","current":"v1.11","minor":"v1.12"},{"service":"x","key":"IMAGE","image":"y","current":"1"}]`
	now := time.Now()
	sweep := func(at time.Time) {
		t.Helper()
		if err := Sweep(context.Background(), c, at); err != nil {
			t.Fatal(err)
		}
	}
	sweep(now) // the weekly updates mail; nothing is red
	got := mails()
	if len(got) != 1 || !strings.Contains(got[0], "To: admin@example.home") || !strings.Contains(got[0], "- whoami: docker.io/traefik/whoami:v1.11 → v1.12") || strings.Contains(got[0], "- x:") {
		t.Fatalf("the weekly updates mail to the operator's address: %q", got)
	}
	if !strings.Contains(got[0], "Laib: Updates verf") {
		t.Errorf("the updates mail's subject changed: %q", got[0])
	}
	f.red = true
	sweep(now.Add(5 * time.Minute))
	if n := len(mails()); n != 1 {
		t.Fatalf("red for less than ten minutes is no mail yet: %d", n)
	}
	sweep(now.Add(15 * time.Minute))
	got = mails()
	if len(got) != 2 || !strings.Contains(got[1], "- whoami whoami.service ist inactive → laib logs whoami") || !strings.Contains(got[1], "Auf der Box ist seit mindestens zehn Minuten rot") {
		t.Fatalf("red for ten minutes is a mail naming the unit: %q", got)
	}
	sweep(now.Add(20 * time.Minute))
	if n := len(mails()); n != 2 {
		t.Fatalf("one mail per incident, not one per sweep: %d", n)
	}
	f.red = false
	sweep(now.Add(25 * time.Minute))
	f.red = true
	sweep(now.Add(30 * time.Minute))
	sweep(now.Add(41 * time.Minute))
	if n := len(mails()); n != 3 {
		t.Fatalf("a new incident is a new mail: %d", n)
	}
	for _, b := range f.bearer {
		if b != "Bearer tok-n" {
			t.Fatalf("an MCP call without the notify token: %q", b)
		}
	}
	if f.refusals != 0 {
		t.Errorf("the notifier called a tool outside its hand")
	}
}

// A failed send is journaled (as a note: the notify hand records, it decides
// nothing) and the incident is mailed again on the next sweep.
func TestAFailedSendIsJournaled(t *testing.T) {
	f := newFakeLaib(t)
	c := f.config(t, "127.0.0.1:1") // nothing listens
	f.red = true
	now := time.Now()
	Sweep(context.Background(), c, now)
	if err := Sweep(context.Background(), c, now.Add(11*time.Minute)); err == nil {
		t.Fatal("a failed send is no error")
	}
	if len(f.notes) == 0 || !strings.HasPrefix(f.notes[len(f.notes)-1], "watch mail an admin@example.home: ") {
		t.Fatalf("the failed send is not journaled: %q", f.notes)
	}
}

// #57, moved with the notifier (#443): while readiness is not red the outside
// watchdog is pinged and the stamp laib's /ready reads is written; a red box
// stays silent on purpose.
func TestThePingWhileNotRed(t *testing.T) {
	f := newFakeLaib(t)
	c := f.config(t, "127.0.0.1:1")
	var pings int
	wd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { pings++ }))
	defer wd.Close()
	c.WatchdogURL = wd.URL + "/ping/abc"
	if !Ping(context.Background(), c) || pings != 1 {
		t.Fatalf("a green box must ping: %d", pings)
	}
	if b, err := os.ReadFile(filepath.Join(c.Home, "watchdog.last")); err != nil || !strings.HasPrefix(string(b), "20") {
		t.Fatalf("no stamp for /ready: %q %v", b, err)
	}
	f.red = true
	if Ping(context.Background(), c) || pings != 1 {
		t.Fatal("a red box must not ping: silence is the alarm")
	}
	c.WatchdogURL = ""
	if Ping(context.Background(), c) {
		t.Fatal("no URL, no ping")
	}
}
