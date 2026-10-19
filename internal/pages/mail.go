package pages

import (
	"cmp"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/registry"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
)

// Mail to the owner (#393): ServiceBay mailed failures, backups and releases;
// with it gone nobody was told. serve sweeps every five minutes (the watchdog's
// tick): a red finding that stays red ten minutes is one mail per incident,
// and once a week the pending image updates. The SMTP settings are the ones
// Authelia has (the sso service's SMTP_* values, AUTHELIA_SMTP_PASSWORD): one
// mail config on the box. To: box.yml notify.mail, else the operator's address.

var RedFor = 10 * time.Minute

type mailSeen struct {
	Red     map[string]time.Time `json:"red"`
	Mailed  map[string]bool      `json:"mailed"`
	Updates time.Time            `json:"updates"`
}

func MailSweep(ctx context.Context, env *cli.Env, st *state.State, now time.Time) error {
	sso := st.Services[st.Box.Roles.SSO]
	if sso == nil || !sso.Set("SMTP_HOST") {
		return nil
	}
	to := cmp.Or(st.Box.Notify.Mail, sso.Values["OPERATOR_EMAIL"].Literal)
	if to == "" {
		return nil
	}
	path := filepath.Join(env.Paths.Home, "mail.json")
	seen := mailSeen{Red: map[string]time.Time{}, Mailed: map[string]bool{}}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &seen)
	}
	red := map[string]string{}
	for _, f := range diag.Probes(ctx, env, st, "") {
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
	var err error
	if len(lines) > 0 {
		sort.Strings(lines)
		if err = sendMail(st, sso, env, to, "Laib: seit 10 Minuten rot", "Auf der Box ist seit mindestens zehn Minuten rot:\n\n"+strings.Join(lines, "\n")+"\n\nlaib diag zeigt alles."); err == nil {
			for _, k := range keys {
				seen.Mailed[k] = true
			}
		}
	}
	if now.Sub(seen.Updates) >= 7*24*time.Hour {
		var ups []string
		for _, u := range registry.ImageUpdates(ctx, st) {
			if u.Patch != "" || u.Minor != "" {
				ups = append(ups, fmt.Sprintf("- %s: %s:%s → %s", u.Service, u.Image, u.Current, cmp.Or(u.Patch, u.Minor)))
			}
		}
		seen.Updates = now
		if len(ups) > 0 {
			if e := sendMail(st, sso, env, to, "Laib: Updates verfügbar", "Neuere Images (laib updates):\n\n"+strings.Join(ups, "\n")+"\n"); e != nil {
				seen.Updates, err = time.Time{}, e
			}
		}
	}
	b, _ := json.Marshal(seen)
	os.WriteFile(path, b, 0o600)
	if err != nil {
		journal.Append(env.Paths.Journal, journal.Entry{Actor: "laib", Verb: "notify", Result: "red", Detail: "mail an " + to + ": " + err.Error()})
	}
	return err
}

// sendMail: SMTP_SECURITY tls is implicit TLS (465); starttls and none
// upgrade when the server offers it.
func sendMail(st *state.State, sso *state.Service, env *cli.Env, to, subject, body string) error {
	v := func(k string) string { return sso.Values[k].Literal }
	host, from := v("SMTP_HOST"), cmp.Or(v("SMTP_FROM"), v("SMTP_USER"))
	addr := net.JoinHostPort(host, cmp.Or(v("SMTP_PORT"), "587"))
	var conn net.Conn
	var err error
	if v("SMTP_SECURITY") == "tls" {
		conn, err = tls.DialWithDialer(&net.Dialer{Timeout: 15 * time.Second}, "tcp", addr, &tls.Config{ServerName: host})
	} else {
		conn, err = net.DialTimeout("tcp", addr, 15*time.Second)
	}
	if err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); ok && v("SMTP_SECURITY") != "tls" {
		if err := c.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return err
		}
	}
	if user := v("SMTP_USER"); user != "" {
		sec, _ := secrets.Load(env.Paths.Secrets, env.Paths.Key)
		if err := c.Auth(smtp.PlainAuth("", user, sec["AUTHELIA_SMTP_PASSWORD"], host)); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "From: Laib <%s>\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s",
		from, to, mime.QEncoding.Encode("utf-8", subject+" ("+st.Box.Domain+")"), time.Now().Format(time.RFC1123Z), strings.ReplaceAll(body, "\n", "\r\n"))
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}
