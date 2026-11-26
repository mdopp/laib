// Command automat is the Bote's MCP tool for Home Assistant automations
// (#26): list, read, write, delete, switch — over HA's REST API, served as
// MCP on loopback inside the bote pod; with AUTOMAT_LAIB_LISTEN also for
// laib-haushalt, Laib's way for the Werkstatt (homes ADR 0004 §3), behind
// the bearer AUTOMAT_TOKEN, in Werkstatt mode (automat.Config.Laib).
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
	addr := env("AUTOMAT_LISTEN", "127.0.0.1:8643") // Hermes, on the pod's loopback: no bearer
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", srv.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	log.Printf("automat: listening on %s, home assistant %s", addr, cfg.HAURL)
	hs := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if laib := os.Getenv("AUTOMAT_LAIB_LISTEN"); laib != "" {
		// the Werkstatt's (laib#1284): every change waits for a code that goes to the person, never in
		// the answer; published on the host's loopback, only with the bearer; its codes are its own
		lcfg := cfg
		lcfg.Laib = true
		bearer := automat.Server(lcfg, Version)
		if bearer.Token = os.Getenv("AUTOMAT_TOKEN"); bearer.Token == "" {
			log.Fatal("automat: AUTOMAT_LAIB_LISTEN needs AUTOMAT_TOKEN")
		}
		lm := http.NewServeMux()
		lm.HandleFunc("/mcp", bearer.Handler())
		log.Printf("automat: for laib on %s, with its bearer", laib)
		go func() {
			log.Fatal((&http.Server{Addr: laib, Handler: lm, ReadHeaderTimeout: 5 * time.Second}).ListenAndServe())
		}()
	}
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
