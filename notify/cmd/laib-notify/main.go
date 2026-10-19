// Command laib-notify runs one pass of the notifier (#443): the outside
// watchdog ping and the owner's mail. laib renders its unit and timer.
package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/mdopp/laib/notify"
)

func main() {
	e := os.Getenv
	c := notify.Config{MCPURL: e("LAIB_MCP_URL"), ReadyURL: e("LAIB_READY_URL"), Token: e("LAIB_TOKEN"), Home: e("LAIB_HOME"),
		WatchdogURL: e("WATCHDOG_URL"), MailTo: e("MAIL_TO"), Domain: e("LAIB_DOMAIN"),
		SMTPHost: e("SMTP_HOST"), SMTPPort: e("SMTP_PORT"), SMTPSecurity: e("SMTP_SECURITY"), SMTPUser: e("SMTP_USER"), SMTPFrom: e("SMTP_FROM"), SMTPPassword: e("SMTP_PASSWORD")}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	notify.Ping(ctx, c)
	if err := notify.Sweep(ctx, c, time.Now()); err != nil {
		log.Println("laib-notify:", err)
	}
}
