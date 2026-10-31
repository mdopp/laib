// Command frage serves "was ist …": SearXNG search plus a model summary,
// as /ask and as an OpenAI-compatible endpoint (ADR 0015).
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/mdopp/laib/frage"
)

func main() {
	n, _ := strconv.Atoi(env("FRAGE_RESULTS", "5"))
	c := frage.Config{
		SearxURL:  env("SEARX_URL", "http://127.0.0.1:8888"),
		ModelURL:  env("MODEL_URL", ""),
		ModelKey:  os.Getenv("MODEL_KEY"),
		ModelName: env("MODEL_NAME", "qwen3.6-35b-a3b"),
		Language:  env("FRAGE_LANGUAGE", "de"),
		Results:   n,
	}
	if c.ModelURL == "" {
		log.Fatal("frage: MODEL_URL is required")
	}
	addr := env("FRAGE_LISTEN", "0.0.0.0:8710")
	log.Printf("frage: listening on %s, searx=%s, model=%s", addr, c.SearxURL, c.ModelName)
	srv := &http.Server{Addr: addr, Handler: frage.Handler(c), ReadHeaderTimeout: 5 * time.Second}
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

// env is the variable, or def when it is unset or empty.
func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
