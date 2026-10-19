// Package notify is the program laib-notify (#443): what tells the owner
// when the box cannot. Every five minutes (its timer, rendered by laib) it
// pings the outside watchdog while laib's readiness is not red (#57), and it
// tells the owner a red that stays ten minutes, once per incident, the
// pending image updates once a week (#393) and each new proposal (#441).
// It tells them through the Bote's channels first (bote-notify, the person
// in box.yml notify.person) and by mail only when no channel is reachable
// (#441, ADR 0016). It reads laib over MCP as the notify hand (diag,
// updates, inbox, note) and /ready; it decides nothing and imports nothing
// of the kernel. The mail settings are Authelia's, which laib renders into
// the unit.
package notify

import (
	"bytes"
	"cmp"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Config is the unit's environment.
type Config struct {
	MCPURL, ReadyURL, Token                                            string
	Home                                                               string // LAIB_HOME: mail.json and watchdog.last, which laib's /ready reads
	WatchdogURL                                                        string
	MailTo, Domain                                                     string
	SMTPHost, SMTPPort, SMTPSecurity, SMTPUser, SMTPFrom, SMTPPassword string
	BoteURL, BoteToken, Person, InboxURL                               string // the chat first (#441)
}

func (c Config) chat() bool { return c.BoteURL != "" && c.BoteToken != "" && c.Person != "" }
func (c Config) mail() bool { return c.SMTPHost != "" && c.MailTo != "" }

// tell is one message to the owner: through the Bote's channels first
// (bote-notify picks the person's first reachable one), by mail only when
// that fails or there is no Bote (#441).
func tell(ctx context.Context, c Config, subject, body string) error {
	var chatErr error
	if c.chat() {
		b, _ := json.Marshal(map[string]string{"an": c.Person, "text": subject + "\n\n" + body})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BoteURL, "/")+"/mitteilung", bytes.NewReader(b))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+c.BoteToken)
			var resp *http.Response
			if resp, err = client.Do(req); err == nil {
				resp.Body.Close()
				if resp.StatusCode/100 == 2 {
					return nil
				}
				err = fmt.Errorf("Bote: %s", resp.Status)
			}
		}
		chatErr = err
	}
	if !c.mail() {
		return chatErr
	}
	return send(c, subject, body)
}

// whom names where a failed message was going, for the journal.
func (c Config) whom() string {
	if c.mail() {
		return "mail an " + c.MailTo
	}
	return "mitteilung an " + c.Person
}

// RedFor is how long a finding stays red before it is mailed.
var RedFor = 10 * time.Minute

var client = &http.Client{Timeout: 2 * time.Minute}

// call runs one of the notify hand's tools and returns its text.
func call(ctx context.Context, c Config, tool string, args map[string]any) (string, error) {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.MCPURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Result struct {
			Content []struct{ Text string } `json:"content"`
			IsError bool                    `json:"isError"`
		} `json:"result"`
		Error *struct{ Message string } `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("%s: %s: %w", tool, resp.Status, err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("%s: %s", tool, out.Error.Message)
	}
	text := ""
	for _, t := range out.Result.Content {
		text += t.Text
	}
	if out.Result.IsError {
		return "", fmt.Errorf("%s: %s", tool, text)
	}
	return text, nil
}

// Ping pings the outside watchdog once (#57); false when nothing was sent
// (no URL, a red or locked box, an error). A sent ping is stamped for /ready.
func Ping(ctx context.Context, c Config) bool {
	if c.WatchdogURL == "" {
		return false
	}
	var r struct {
		State string `json:"state"`
		Red   int    `json:"red"`
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", c.ReadyURL, nil)
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	err = json.NewDecoder(resp.Body).Decode(&r)
	resp.Body.Close()
	if err != nil || r.Red > 0 || r.State == "eingeschränkt" {
		return false
	}
	req, err = http.NewRequestWithContext(ctx, "GET", c.WatchdogURL, nil)
	if err != nil {
		return false
	}
	resp, err = (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return false
	}
	os.WriteFile(filepath.Join(c.Home, "watchdog.last"), []byte(time.Now().UTC().Format(time.RFC3339)), 0o644)
	return true
}

type mailSeen struct {
	Red       map[string]time.Time `json:"red"`
	Mailed    map[string]bool      `json:"mailed"`
	Updates   time.Time            `json:"updates"`
	Proposals map[int]bool         `json:"proposals"` // announced once (#441)
}

// Sweep mails a red that stayed RedFor, once per incident, and once a week
// the pending image updates (#393). A failed send is journaled (laib note)
// and tried again on the next sweep.
func Sweep(ctx context.Context, c Config, now time.Time) error {
	if !c.chat() && !c.mail() {
		return nil
	}
	path := filepath.Join(c.Home, "mail.json")
	seen := mailSeen{Red: map[string]time.Time{}, Mailed: map[string]bool{}, Proposals: map[int]bool{}}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &seen)
	}
	if seen.Proposals == nil {
		seen.Proposals = map[int]bool{}
	}
	text, err := call(ctx, c, "laib_diag", map[string]any{})
	if err != nil {
		return err
	}
	var findings []struct{ Severity, Probe, Service, Detail, Fix string }
	if err := json.Unmarshal([]byte(text), &findings); err != nil {
		return fmt.Errorf("laib_diag: %w", err)
	}
	red := map[string]string{}
	for _, f := range findings {
		if f.Severity == "rot" {
			k, line := f.Probe+" "+f.Service, "- "+strings.TrimSpace(f.Service+" "+f.Detail)
			if f.Fix != "" {
				line += " → " + f.Fix
			}
			red[k] = strings.TrimSpace(red[k] + "\n" + line)
		}
	}
	var lines, keys []string
	for k, d := range red {
		if seen.Red[k].IsZero() {
			seen.Red[k] = now
		}
		if now.Sub(seen.Red[k]) >= RedFor && !seen.Mailed[k] {
			lines, keys = append(lines, d), append(keys, k)
		}
	}
	for k := range seen.Red {
		if _, ok := red[k]; !ok {
			delete(seen.Red, k)
			delete(seen.Mailed, k)
		}
	}
	if len(lines) > 0 {
		sort.Strings(lines)
		if err = tell(ctx, c, "Laib: seit 10 Minuten rot", "Auf der Box ist seit mindestens zehn Minuten rot:\n\n"+strings.Join(lines, "\n")+"\n\nlaib diag zeigt alles."); err == nil {
			for _, k := range keys {
				seen.Mailed[k] = true
			}
		}
	}
	if now.Sub(seen.Updates) >= 7*24*time.Hour {
		var ups []string
		var list []struct{ Service, Image, Current, Patch, Minor string }
		if text, e := call(ctx, c, "laib_updates", map[string]any{}); e == nil && json.Unmarshal([]byte(text), &list) == nil {
			for _, u := range list {
				if u.Patch != "" || u.Minor != "" {
					ups = append(ups, fmt.Sprintf("- %s: %s:%s → %s", u.Service, u.Image, u.Current, cmp.Or(u.Patch, u.Minor)))
				}
			}
		}
		seen.Updates = now
		if len(ups) > 0 {
			if e := tell(ctx, c, "Laib: Updates verfügbar", "Neuere Images (laib updates):\n\n"+strings.Join(ups, "\n")+"\n"); e != nil {
				seen.Updates, err = time.Time{}, e
			}
		}
	}
	if e := proposals(ctx, c, seen.Proposals); e != nil {
		err = e
	}
	b, _ := json.Marshal(seen)
	os.WriteFile(path, b, 0o600)
	if err != nil {
		// watch, not incident: a failed message must not wake the Operator into a loop
		call(ctx, c, "laib_note", map[string]any{"kind": "watch", "text": c.whom() + ": " + err.Error()})
	}
	return err
}

// proposals tells the owner each open proposal once (#441). One that is not
// destructive says how to decide it in the chat (the relay acts on the
// owner's verified identity); a destroy, a removal or what the review marks
// for a human only links the Inbox, where Authelia asks for the second
// factor: the chat never approves those (ADR 0016).
func proposals(ctx context.Context, c Config, seen map[int]bool) error {
	text, err := call(ctx, c, "laib_inbox", map[string]any{})
	if err != nil {
		return err
	}
	var open []struct {
		ID                  int
		Reason, Tier, Class string
		Card                struct {
			Summary    string
			Impact     []string
			Reversible string
		}
	}
	if err := json.Unmarshal([]byte(text), &open); err != nil {
		return fmt.Errorf("laib_inbox: %w", err)
	}
	isOpen := map[int]bool{}
	for _, p := range open {
		isOpen[p.ID] = true
		if seen[p.ID] {
			continue
		}
		// the decision line comes second: bote-notify cuts a long text at the end
		body := cmp.Or(p.Card.Summary, p.Reason)
		if p.Tier == "destroy" || p.Class == "human" || p.Class == "remove" {
			body += "\nDas entscheidest du nur in der Inbox, mit dem zweiten Faktor: " + c.InboxURL
		} else {
			body += fmt.Sprintf("\nIm Chat: »gib #%d frei« oder »lehne #%d ab«. Oder in der Inbox: %s", p.ID, p.ID, c.InboxURL)
		}
		for _, i := range p.Card.Impact {
			body += "\n- " + i
		}
		if p.Card.Reversible != "" {
			body += "\nRückgängig: " + p.Card.Reversible
		}
		if e := tell(ctx, c, fmt.Sprintf("Laib: Vorschlag #%d", p.ID), body); e != nil {
			return e
		}
		seen[p.ID] = true
	}
	for id := range seen { // decided ones are forgotten, so the file stays small
		if !isOpen[id] {
			delete(seen, id)
		}
	}
	return nil
}

// send: SMTP_SECURITY tls is implicit TLS (465); starttls and none upgrade
// when the server offers it.
func send(c Config, subject, body string) error {
	host, from := c.SMTPHost, cmp.Or(c.SMTPFrom, c.SMTPUser)
	addr := net.JoinHostPort(host, cmp.Or(c.SMTPPort, "587"))
	var conn net.Conn
	var err error
	if c.SMTPSecurity == "tls" {
		conn, err = tls.DialWithDialer(&net.Dialer{Timeout: 15 * time.Second}, "tcp", addr, &tls.Config{ServerName: host})
	} else {
		conn, err = net.DialTimeout("tcp", addr, 15*time.Second)
	}
	if err != nil {
		return err
	}
	s, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer s.Close()
	if ok, _ := s.Extension("STARTTLS"); ok && c.SMTPSecurity != "tls" {
		if err := s.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return err
		}
	}
	if c.SMTPUser != "" {
		if err := s.Auth(smtp.PlainAuth("", c.SMTPUser, c.SMTPPassword, host)); err != nil {
			return err
		}
	}
	if err := s.Mail(from); err != nil {
		return err
	}
	if err := s.Rcpt(c.MailTo); err != nil {
		return err
	}
	w, err := s.Data()
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "From: Laib <%s>\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s",
		from, c.MailTo, mime.QEncoding.Encode("utf-8", subject+" ("+c.Domain+")"), time.Now().Format(time.RFC1123Z), strings.ReplaceAll(body, "\n", "\r\n"))
	if err := w.Close(); err != nil {
		return err
	}
	return s.Quit()
}
