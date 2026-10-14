// Command relay is the Bote's approval path (#48, #184): it listens to the
// Signal daemon and reads the admin's Matrix room, acts only on the admin's
// number or Matrix ID, and decides through Laib with the approver token.
// Hermes holds no token that can approve; Telegram never approves.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/relay"
)

var Version = "dev"

func main() {
	cfg := relay.Config{
		Admin:     os.Getenv("ADMIN_NUMBER"),
		AdminMXID: os.Getenv("ADMIN_MXID"),
		Room:      os.Getenv("ADMIN_ROOM"),
		Account:   os.Getenv("SIGNAL_ACCOUNT"),
		SignalURL: boxfs.Env("SIGNAL_HTTP_URL", "http://127.0.0.1:8080"),
		LaibURL:   boxfs.Env("LAIB_MCP_URL", "http://host.containers.internal:5900/mcp"),
		Token:     os.Getenv("LAIB_TOKEN"),
	}
	switch {
	case cfg.Token == "" || cfg.Admin == "" && cfg.AdminMXID == "":
		log.Fatal("relay: LAIB_TOKEN and ADMIN_NUMBER or ADMIN_MXID are required")
	case cfg.Admin != "" && cfg.Account == "":
		log.Fatal("relay: ADMIN_NUMBER needs SIGNAL_ACCOUNT (the Bote on Signal)")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	out := relay.Senders{}
	var mx *relay.Matrix
	if cfg.AdminMXID != "" {
		mx = &relay.Matrix{URL: os.Getenv("MATRIX_HOMESERVER"), Token: os.Getenv("MATRIX_ACCESS_TOKEN"), Room: cfg.Room}
		if err := mx.Check(ctx, cfg.AdminMXID); err != nil {
			log.Fatalf("relay: Matrix approvals refused (ADR 0016 §3): %v", err)
		}
		out.Matrix = mx
	}
	if cfg.Admin != "" {
		out.Signal = &relay.SignalSender{URL: cfg.SignalURL, Account: cfg.Account}
	}
	r := relay.New(cfg, out)
	log.Printf("relay %s: signal admin %q, matrix admin %q in %q, laib %s", Version, cfg.Admin, cfg.AdminMXID, cfg.Room, cfg.LaibURL)
	if mx != nil {
		go r.RunMatrix(ctx, mx)
	}
	if cfg.Admin != "" {
		go r.Run(ctx)
	}
	<-ctx.Done()
}
