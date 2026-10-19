package main

import (
	"bufio"
	"context"
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

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/pages"
	"github.com/mdopp/laib/internal/registry"
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

// #393: since ServiceBay's mails are gone nobody is told. Laib mails the
// owner, with the SMTP settings Authelia already has (no second mail
// config): a service red for ten minutes, once per incident; and once a week
// the pending image updates.
func TestTheOwnerGetsAMailWhenAServiceIsRedForTenMinutes(t *testing.T) {
	b := newRbox(t)
	addr, mails := fakeSMTP(t)
	host, port, _ := net.SplitHostPort(addr)
	vals := filepath.Join(b.env.Paths.State, "services", "auth", "values.yml")
	raw, _ := os.ReadFile(vals)
	os.WriteFile(vals, append(raw, []byte(fmt.Sprintf("SMTP_HOST: %s\nSMTP_PORT: \"%s\"\nSMTP_SECURITY: none\nSMTP_USER: \"\"\nSMTP_FROM: box@example.home\n", host, port))...), 0o644)
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tags":["v1.11","v1.11.1","v1.12"]}`)
	}))
	defer reg.Close()
	realURL := registry.RegistryURL
	t.Cleanup(func() { registry.RegistryURL = realURL })
	registry.RegistryURL = func(string) string { return reg.URL }

	st, err := cli.LoadState(b.env)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	sweep := func(at time.Time) {
		t.Helper()
		if err := pages.MailSweep(context.Background(), b.env, st, at); err != nil {
			t.Fatal(err)
		}
	}
	sweep(now) // the weekly updates mail; nothing is red
	got := mails()
	if len(got) != 1 || !strings.Contains(got[0], "To: admin@example.home") || !strings.Contains(got[0], "whoami") || !strings.Contains(got[0], "v1.12") {
		t.Fatalf("the weekly updates mail to the operator's address: %q", got)
	}
	b.down["whoami.service"] = true // a stopped test service
	sweep(now.Add(5 * time.Minute))
	if n := len(mails()); n != 1 {
		t.Fatalf("red for less than ten minutes is no mail yet: %d", n)
	}
	sweep(now.Add(15 * time.Minute))
	got = mails()
	if len(got) != 2 || !strings.Contains(got[1], "whoami.service") {
		t.Fatalf("red for ten minutes is a mail naming the unit: %q", got)
	}
	sweep(now.Add(20 * time.Minute))
	if n := len(mails()); n != 2 {
		t.Fatalf("one mail per incident, not one per sweep: %d", n)
	}
	delete(b.down, "whoami.service")
	sweep(now.Add(25 * time.Minute))
	b.down["whoami.service"] = true
	sweep(now.Add(30 * time.Minute))
	sweep(now.Add(41 * time.Minute))
	if n := len(mails()); n != 3 {
		t.Fatalf("a new incident is a new mail: %d", n)
	}
}

// #393: a failed or stale nightly backup is red in diag, so it reaches the owner's mail.
func TestAFailedOrStaleBackupIsRed(t *testing.T) {
	b := newRbox(t)
	st, err := cli.LoadState(b.env)
	if err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(b.env.Paths.Journal)
	journal.Append(b.env.Paths.Journal, journal.Entry{T: time.Now().Add(-72 * time.Hour), Actor: "human", Verb: "init", Result: "green"})
	stale := func() bool {
		for _, f := range diag.Probes(context.Background(), b.env, st, "") {
			if f.Probe == "backup" && strings.Contains(f.Detail, "letzte Sicherung") {
				return f.Severity == "rot"
			}
		}
		return false
	}
	if !stale() {
		t.Fatal("three days without a backup must be red")
	}
	journal.Append(b.env.Paths.Journal, journal.Entry{Actor: "laib", Verb: "backup", Result: "green"})
	if stale() {
		t.Fatal("a fresh green backup is no finding")
	}
	journal.Append(b.env.Paths.Journal, journal.Entry{Actor: "laib", Verb: "backup", Result: "red"})
	if !stale() {
		t.Fatal("a failed last backup must be red")
	}
}
