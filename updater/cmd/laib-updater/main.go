// Command laib-updater answers the kernel's laib updates (ADR 0035, #1233)
// in its own container (ADR 0027): HTTP on UPDATER_LISTEN (127.0.0.1:5903),
// each call with the door's key (UPDATER_KEY); the registries and GitHub's
// API (GITHUB_API_URL, default api.github.com) over the net, cosign from its
// own image. It holds no token of laib's and calls laib for nothing.
package main

import (
	"cmp"
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mdopp/laib/updater"
)

func main() {
	key := os.Getenv("UPDATER_KEY")
	if key == "" {
		log.Fatal("laib-updater: UPDATER_KEY is empty: anyone on the box could ask it as laib")
	}
	addr := cmp.Or(os.Getenv("UPDATER_LISTEN"), "127.0.0.1:5903")
	log.Printf("laib-updater: %s", addr)
	env := &updater.Env{GitHub: updater.GitHub{API: os.Getenv("GITHUB_API_URL")}}
	srv := &http.Server{Addr: addr, Handler: updater.Handler(key, env), ReadHeaderTimeout: 5 * time.Second}
	// a planned stop is no failure (#956): stop accepting, give what runs 5 s, exit 0
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}
