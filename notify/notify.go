// Package notify is the program laib-notify (#443): what tells the owner
// when the box cannot. Every five minutes (its timer, rendered by laib) it
// pings the outside watchdog while laib's readiness is not red (#57), and it
// mails the owner a red that stays ten minutes, once per incident, and the
// pending image updates once a week (#393). It reads laib over MCP as the
// notify hand (diag, updates, note) and /ready; it decides nothing and
// imports nothing of the kernel. The mail settings are Authelia's, which
// laib renders into the unit.
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
	Red     map[string]time.Time `json:"red"`
	Mailed  map[string]bool      `json:"mailed"`
	Updates time.Time            `json:"updates"`
}

// Sweep mails a red that stayed RedFor, once per incident, and once a week
// the pending image updates (#393). A failed send is journaled (laib note)
// and tried again on the next sweep.
func Sweep(ctx context.Context, c Config, now time.Time) error {
	if c.SMTPHost == "" || c.MailTo == "" {
		return nil
	}
	path := filepath.Join(c.Home, "mail.json")
	seen := mailSeen{Red: map[string]time.Time{}, Mailed: map[string]bool{}}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &seen)
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
		if err = send(c, "Laib: seit 10 Minuten rot", "Auf der Box ist seit mindestens zehn Minuten rot:\n\n"+strings.Join(lines, "\n")+"\n\nlaib diag zeigt alles."); err == nil {
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
			if e := send(c, "Laib: Updates verfügbar", "Neuere Images (laib updates):\n\n"+strings.Join(ups, "\n")+"\n"); e != nil {
				seen.Updates, err = time.Time{}, e
			}
		}
	}
	b, _ := json.Marshal(seen)
	os.WriteFile(path, b, 0o600)
	if err != nil {
		// watch, not incident: a failed mail must not wake the Operator into a loop
		call(ctx, c, "laib_note", map[string]any{"kind": "watch", "text": "mail an " + c.MailTo + ": " + err.Error()})
	}
	return err
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
