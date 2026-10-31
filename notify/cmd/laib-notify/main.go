// Command laib-notify runs one pass of the notifier (#443): the outside
// watchdog ping and the owner's messages, through the Bote first, mail as
// fallback (#441). laib renders its unit and timer.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mdopp/laib/notify"
)

func main() {
	e := os.Getenv
	c := notify.Config{MCPURL: e("LAIB_MCP_URL"), ReadyURL: e("LAIB_READY_URL"), Token: e("LAIB_TOKEN"), Home: e("NOTIFY_HOME"),
		WatchdogURL: e("WATCHDOG_URL"), MailTo: e("MAIL_TO"), Domain: e("LAIB_DOMAIN"),
		SMTPHost: e("SMTP_HOST"), SMTPPort: e("SMTP_PORT"), SMTPSecurity: e("SMTP_SECURITY"), SMTPUser: e("SMTP_USER"), SMTPFrom: e("SMTP_FROM"), SMTPPassword: e("SMTP_PASSWORD"),
		BoteURL: e("BOTE_NOTIFY_URL"), BoteToken: e("BOTE_NOTIFY_TOKEN"), Person: e("NOTIFY_PERSON"), InboxURL: e("LAIB_INBOX_URL")}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM) // a stop ends the pass as its limit does (#956)
	defer stop()
	notify.Ping(ctx, c)
	if err := notify.Sweep(ctx, c, time.Now()); err != nil {
		log.Println("laib-notify:", err)
	}
}
