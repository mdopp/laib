// Command relay is the Bote's approval path (#48, #184): it listens to the
// Signal daemon and reads the admin's Matrix room, acts only on the admin's
// number, Matrix ID or Discord id (#463), and decides through Laib with the
// approver token. Hermes holds no token that can approve; Telegram never approves.
package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/mdopp/laib/internal/relay"
)

var Version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := run(ctx, os.Getenv); err != nil {
		log.Fatal("relay: ", err)
	}
}

// run serves until the context ends. Matrix never takes Signal down: a
// failed Matrix check is journaled and retried while Signal goes on (#184).
func run(ctx context.Context, getenv func(string) string) error {
	env := func(k, def string) string {
		if v := getenv(k); v != "" {
			return v
		}
		return def
	}
	cfg := relay.Config{
		Admin:     getenv("ADMIN_NUMBER"),
		AdminMXID: getenv("ADMIN_MXID"),
		Room:      getenv("ADMIN_ROOM"),
		// the owner's Discord id and their DM with the bot (#463)
		AdminDiscord: getenv("ADMIN_DISCORD_ID"), DiscordChannel: getenv("ADMIN_DISCORD_CHANNEL"),
		Account:   getenv("SIGNAL_ACCOUNT"),
		SignalURL: env("SIGNAL_HTTP_URL", "http://127.0.0.1:8080"),
		LaibURL:   env("LAIB_MCP_URL", "http://host.containers.internal:5900/mcp"),
		Token:     getenv("LAIB_TOKEN"),
	}
	switch {
	case cfg.Token == "" || cfg.Admin == "" && cfg.AdminMXID == "" && cfg.AdminDiscord == "":
		return errors.New("LAIB_TOKEN and ADMIN_NUMBER, ADMIN_MXID or ADMIN_DISCORD_ID are required")
	case cfg.Admin != "" && cfg.Account == "":
		return errors.New("ADMIN_NUMBER needs SIGNAL_ACCOUNT (the Bote on Signal)")
	}
	out := relay.Senders{}
	var mx *relay.Matrix
	if cfg.AdminMXID != "" {
		mx = &relay.Matrix{URL: getenv("MATRIX_HOMESERVER"), Token: getenv("MATRIX_ACCESS_TOKEN"), Room: cfg.Room}
		out.Matrix = mx
	}
	if cfg.Admin != "" {
		out.Signal = &relay.SignalSender{URL: cfg.SignalURL, Account: cfg.Account}
	}
	var dc *relay.Discord
	if cfg.AdminDiscord != "" {
		dc = &relay.Discord{API: getenv("DISCORD_API_URL"), Token: getenv("DISCORD_BOT_TOKEN"), Channel: cfg.DiscordChannel}
		out.Discord = dc
	}
	r := relay.New(cfg, out)
	log.Printf("relay %s: signal admin %q, matrix admin %q in %q, laib %s", Version, cfg.Admin, cfg.AdminMXID, cfg.Room, cfg.LaibURL)
	if mx != nil {
		go r.WatchMatrix(ctx, mx, cfg.AdminMXID)
	}
	if dc != nil {
		go r.WatchDiscord(ctx, dc, cfg.AdminDiscord)
	}
	if cfg.Admin != "" {
		go r.Run(ctx)
	}
	<-ctx.Done()
	return nil
}
