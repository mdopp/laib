// Package notify is the program laib-notify (#443): what tells the owner
// when the box cannot. Every five minutes (its timer, rendered by laib) it
// pings the outside watchdog while laib's readiness is not red (#57), and it
// tells the owner a red that stays ten minutes, once per incident, what is
// behind once a week (#393, "Was ist veraltet" #528) and each new proposal
// (#441), and what a template could not get to them itself (eingang/,
// #703). It tells them through the Bote's channels first (bote-notify, the person
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
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/mail"
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
	Home                                                               string // NOTIFY_HOME, its own folder (ADR 0027): mail.json and watchdog.last, which laib's /ready reads; eingang/ (#703)
	WatchdogURL                                                        string
	MailTo, Domain                                                     string
	SMTPHost, SMTPPort, SMTPSecurity, SMTPUser, SMTPFrom, SMTPPassword string
	BoteURL, BoteToken, Person, InboxURL                               string // the chat first (#441)
}

func (c Config) chat() bool { return c.BoteURL != "" && c.BoteToken != "" && c.Person != "" }
func (c Config) mail() bool { return c.SMTPHost != "" && c.MailTo != "" }

// run is one sweep: its time, the state it reads and writes, and which
// messages it tried, so the failures of the others are forgotten.
type run struct {
	ctx   context.Context
	c     Config
	now   time.Time
	seen  *mailSeen
	tried map[string]bool
}

// tell is one message to the owner, named by its subject: through the Bote's
// channels first (bote-notify picks the person's first reachable one), by
// mail only when that fails or there is no Bote (#441), once, and not after
// GiveUp since its first failure (#659). An empty body makes the subject
// the whole message, in the chat and in the mail (#778). chat: the chat took
// it; err nil: the owner has it, or with no chat never will.
func (r *run) tell(subject, body string) (chat bool, err error) {
	c, s := r.c, r.seen
	r.tried[subject] = true
	if c.chat() {
		if err = post(r.ctx, c, subject, body); err == nil {
			return true, nil
		}
		r.note("mitteilung an "+c.Person, err)
		if errors.Is(err, errBusy) { // the Bote is there: the next run, not the mail
			return false, err
		}
	}
	if s.Failing[subject].IsZero() {
		s.Failing[subject] = r.now
	}
	if !c.mail() || s.ByMail[subject] {
		return false, err
	}
	if r.now.Sub(s.Failing[subject]) >= GiveUp {
		s.ByMail[subject] = true
		r.note("mail an "+c.MailTo, fmt.Errorf("„%s“ seit %s nicht zugestellt, keine weitere Mail", subject, GiveUp))
		if c.chat() {
			return false, err
		}
		return false, nil
	}
	if e := send(c, subject, body); e != nil {
		r.note("mail an "+c.MailTo, e)
		return false, e
	}
	s.ByMail[subject] = true
	return false, nil
}

// note journals a failure, each distinct one once an hour (#659): watch, not
// incident: a failed message must not wake the Operator into a loop.
func (r *run) note(whom string, err error) {
	text := whom + ": " + err.Error()
	if r.now.Sub(r.seen.Noted[text]) < time.Hour {
		return
	}
	r.seen.Noted[text] = r.now
	call(r.ctx, r.c, "laib_note", map[string]any{"kind": "watch", "text": text})
}

// bote-notify's limits (templates/bote/notify.py): a body over maxBody is
// refused with 413 before anything is logged, a text over maxText is cut.
const maxBody, maxText = 4096, 1000

var errBusy = errors.New("Bote: 429, zu viele Mitteilungen in dieser Minute")

// post sends one message to bote-notify, its text cut until it fits.
func post(ctx context.Context, c Config, subject, body string) error {
	text := subject
	if body != "" {
		text += "\n\n" + body
	}
	var b bytes.Buffer
	for n := maxText; ; n = n * 9 / 10 {
		msg := map[string]any{"an": c.Person, "text": cut(text, n)}
		b.Reset()
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		enc.Encode(msg)
		if b.Len() <= maxBody || n < 100 {
			break
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BoteURL, "/")+"/mitteilung", &b)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.BoteToken)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 2 {
		return nil
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return errBusy
	}
	why, _ := io.ReadAll(io.LimitReader(resp.Body, 200)) // bote-notify's "fehler"
	return fmt.Errorf("Bote: %s %s", resp.Status, bytes.TrimSpace(why))
}

// cut shortens s to n characters, the last one "…".
func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// RedFor is how long a finding stays red before it is mailed.
var RedFor = 10 * time.Minute

// GiveUp is how long a failing message is still mailed (#659); the chat
// is tried for as long as the message stands.
var GiveUp = 24 * time.Hour

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
	Proposals map[int]bool         `json:"proposals"` // reached the chat, or the mail where there is none (#441, #659)
	Failing   map[string]time.Time `json:"failing"`   // a message's first failed send, by its subject (#659)
	ByMail    map[string]bool      `json:"byMail"`    // mailed, or given up on: the chat alone tries again
	Noted     map[string]time.Time `json:"noted"`     // a failure's last journal note, by its text
}

// Sweep mails a red that stayed RedFor, once per incident, and once a week
// the pending image updates (#393). A failed send is journaled (laib note)
// and tried again on the next sweep.
func Sweep(ctx context.Context, c Config, now time.Time) error {
	if !c.chat() && !c.mail() {
		return nil
	}
	path := filepath.Join(c.Home, "mail.json")
	seen := mailSeen{Red: map[string]time.Time{}, Mailed: map[string]bool{}, Proposals: map[int]bool{},
		Failing: map[string]time.Time{}, ByMail: map[string]bool{}, Noted: map[string]time.Time{}}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &seen)
	}
	r := &run{ctx: ctx, c: c, now: now, seen: &seen, tried: map[string]bool{}}
	r.eingang()
	text, err := call(ctx, c, "laib_diag", map[string]any{})
	if err != nil {
		return err
	}
	var findings []struct{ Severity, Probe, Service, Detail, Fix, Say string }
	if err := json.Unmarshal([]byte(text), &findings); err != nil {
		return fmt.Errorf("laib_diag: %w", err)
	}
	red, says := map[string]string{}, map[string]string{} // the raw lines for Details, the plain sentence on top (#855)
	for _, f := range findings {
		if f.Severity == "rot" {
			k, line := f.Probe+" "+f.Service, "- "+strings.TrimSpace(f.Service+" "+f.Detail)
			if f.Fix != "" {
				line += " → " + f.Fix
			}
			red[k] = strings.TrimSpace(red[k] + "\n" + line)
			says[k] = "- " + cmp.Or(f.Say, strings.TrimSpace(f.Service+" "+f.Detail))
		}
	}
	var lines, details, keys []string
	for k, d := range red {
		if seen.Red[k].IsZero() {
			seen.Red[k] = now
		}
		if now.Sub(seen.Red[k]) >= RedFor && !seen.Mailed[k] {
			lines, details, keys = append(lines, says[k]), append(details, d), append(keys, k)
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
		sort.Strings(details)
		if _, err = r.tell("Laib: seit 10 Minuten rot", "Auf der Box ist seit mindestens zehn Minuten rot:\n\n"+strings.Join(lines, "\n")+
			"\n\nWas du tun kannst: Sieh auf der Box-Seite unter Status nach; liegt in der Inbox eine Reparatur, gib sie frei.\n\nDetails für den, der sich darum kümmert:\n"+strings.Join(details, "\n")); err == nil {
			for _, k := range keys {
				seen.Mailed[k] = true
			}
		}
	}
	if now.Sub(seen.Updates) >= 7*24*time.Hour {
		// "Was ist veraltet" (#528): the images by tag, pin or digest and the
		// host, then what diag sees 30 days behind and what a service reports
		// itself (its extensions, the devices' firmware)
		var ups []string
		var list []struct{ Service, Image, Current, Patch, Minor, Digest string }
		if text, e := call(ctx, c, "laib_updates", map[string]any{}); e == nil && json.Unmarshal([]byte(text), &list) == nil {
			for _, u := range list {
				if w := cmp.Or(u.Patch, u.Minor, u.Digest); w != "" {
					ups = append(ups, fmt.Sprintf("- %s: %s:%s → %s", u.Service, u.Image, u.Current, w))
				}
			}
		}
		for _, f := range findings {
			if f.Probe == "updates" && f.Service != "" || f.Probe == "status" && (strings.HasPrefix(f.Detail, "Update: ") || strings.HasPrefix(f.Detail, "Firmware: ")) {
				ups = append(ups, "- "+f.Service+": "+f.Detail)
			}
		}
		seen.Updates = now
		if len(ups) > 0 {
			if _, e := r.tell("Laib: Was ist veraltet", "Was ist veraltet (laib updates, laib diag):\n\n"+strings.Join(ups, "\n")+"\n"); e != nil {
				seen.Updates, err = time.Time{}, e
			}
		}
	}
	if e := r.proposals(); e != nil {
		err = e
	}
	for k := range seen.Failing { // a message no longer tried is no longer failing
		if !r.tried[k] {
			delete(seen.Failing, k)
			delete(seen.ByMail, k)
		}
	}
	for k, t := range seen.Noted {
		if now.Sub(t) >= time.Hour {
			delete(seen.Noted, k)
		}
	}
	b, _ := json.Marshal(seen)
	os.WriteFile(path, b, 0o600)
	return err
}

// eingang tells what a template handed over because its own way to the
// owner failed (#703: the failed-login counter while the Bote is away), one
// JSON file per message in Home/eingang, {"betreff", "text", "at"}: as the
// rest, the Bote first, else one mail. The file goes once it is told, or
// after a day; one that is unreadable at once.
func (r *run) eingang() {
	files, _ := filepath.Glob(filepath.Join(r.c.Home, "eingang", "*.json"))
	for _, f := range files {
		var m struct {
			Betreff, Text string
			At            int64
		}
		b, err := os.ReadFile(f)
		if err != nil || json.Unmarshal(b, &m) != nil || strings.TrimSpace(m.Text) == "" || r.now.Sub(time.Unix(m.At, 0)) > GiveUp {
			os.Remove(f)
			continue
		}
		if chat, err := r.tell("Laib: "+cmp.Or(m.Betreff, "Mitteilung"), m.Text); chat || err == nil {
			os.Remove(f)
		}
	}
}

// proposals tells the owner each open proposal once (#441), as one line
// (#778, owner 2026-10-26): „Freigabe #N: <headline> · <Inbox>“. The Inbox
// is the only place a proposal is decided (ADR 0012), so the message carries
// nothing to decide with: no impact lines, no decision words, no poll.
func (r *run) proposals() error {
	c, seen := r.c, r.seen.Proposals
	text, err := call(r.ctx, c, "laib_inbox", map[string]any{})
	if err != nil {
		r.note("laib-notify", err)
		return err
	}
	var open []struct {
		ID     int
		Reason string
		Card   struct{ Summary string }
	}
	if err := json.Unmarshal([]byte(text), &open); err != nil {
		err = fmt.Errorf("laib_inbox: %w", err)
		r.note("laib-notify", err)
		return err
	}
	isOpen := map[int]bool{}
	for _, p := range open {
		isOpen[p.ID] = true
		if seen[p.ID] {
			continue
		}
		headline := strings.Join(strings.Fields(cmp.Or(p.Card.Summary, p.Reason)), " ")
		// one that fails blocks no other; it is told again on the next run,
		// on the chat until the chat takes it (#659)
		chat, e := r.tell(fmt.Sprintf("Freigabe #%d: %s · %s", p.ID, headline, c.InboxURL), "")
		if chat || e == nil && !c.chat() {
			seen[p.ID] = true
		} else if e != nil {
			err = e
		}
	}
	for id := range seen { // decided ones are forgotten, so the file stays small
		if !isOpen[id] {
			delete(seen, id)
		}
	}
	return err
}

// send: SMTP_SECURITY tls is implicit TLS (465); starttls and none upgrade
// when the server offers it. An empty body: the subject is the whole mail,
// subject and body alike (#778).
func send(c Config, subject, body string) error {
	if body == "" {
		body = subject
	} else {
		subject += " (" + c.Domain + ")"
	}
	// SMTP_FROM is Authelia's sender, "Name <addr>": the envelope takes the
	// bare address, else Gmail answers 555 5.5.2 (#659)
	from, err := mail.ParseAddress(cmp.Or(c.SMTPFrom, c.SMTPUser))
	if err != nil {
		return fmt.Errorf("SMTP_FROM: %w", err)
	}
	to, err := mail.ParseAddress(c.MailTo)
	if err != nil {
		return fmt.Errorf("MAIL_TO: %w", err)
	}
	from.Name = cmp.Or(from.Name, "Laib")
	host := c.SMTPHost
	addr := net.JoinHostPort(host, cmp.Or(c.SMTPPort, "587"))
	var conn net.Conn
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
	if err := s.Mail(from.Address); err != nil {
		return err
	}
	if err := s.Rcpt(to.Address); err != nil {
		return err
	}
	w, err := s.Data()
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s",
		from, c.MailTo, mime.QEncoding.Encode("utf-8", subject), time.Now().Format(time.RFC1123Z), strings.ReplaceAll(body, "\n", "\r\n"))
	if err := w.Close(); err != nil {
		return err
	}
	return s.Quit()
}
