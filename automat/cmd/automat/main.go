// Command automat is the Bote's MCP tool for Home Assistant automations
// (#26): list, read, write, delete, switch — over HA's REST API, served as
// MCP on loopback inside the bote pod.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mdopp/laib/automat"
)

var Version = "dev"

func main() {
	cfg := automat.Config{HAURL: env("HA_URL", "http://host.containers.internal:8123"), Token: os.Getenv("HA_TOKEN"), Data: env("AUTOMAT_DATA", "/data")}
	if cfg.Token == "" {
		log.Fatal("automat: HA_TOKEN is required")
	}
	srv := automat.Server(cfg, Version)
	srv.Token = os.Getenv("AUTOMAT_TOKEN") // optional; loopback in the pod needs none
	addr := env("AUTOMAT_LISTEN", "127.0.0.1:8643")
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", srv.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	log.Printf("automat: listening on %s, home assistant %s", addr, cfg.HAURL)
	hs := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	// a planned stop is no failure (#956): stop accepting, give what runs 5 s, exit 0
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		if err := hs.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hs.Shutdown(ctx)
}

// env is the variable, or def when it is unset or empty.
func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
