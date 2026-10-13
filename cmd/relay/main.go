// Command relay is the Bote's approval path (#48): it listens to the Signal
// daemon, acts only on the admin's number, and decides through Laib with
// the approver token. Hermes holds no token that can approve.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/mdopp/laib/internal/relay"
)

var Version = "dev"

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	cfg := relay.Config{
		Admin:     os.Getenv("ADMIN_NUMBER"),
		Account:   os.Getenv("SIGNAL_ACCOUNT"),
		SignalURL: env("SIGNAL_HTTP_URL", "http://127.0.0.1:8080"),
		LaibURL:   env("LAIB_MCP_URL", "http://host.containers.internal:5900/mcp"),
		Token:     os.Getenv("LAIB_TOKEN"),
	}
	if cfg.Admin == "" || cfg.Account == "" || cfg.Token == "" {
		log.Fatal("relay: ADMIN_NUMBER, SIGNAL_ACCOUNT and LAIB_TOKEN are required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	log.Printf("relay %s: admin %s, signal %s, laib %s", Version, cfg.Admin, cfg.SignalURL, cfg.LaibURL)
	relay.New(cfg, &relay.SignalSender{URL: cfg.SignalURL, Account: cfg.Account}).Run(ctx)
}
