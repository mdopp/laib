// Command frage serves "was ist …": SearXNG search plus a model summary,
// as /ask and as an OpenAI-compatible endpoint (ADR 0015).
package main

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/frage"
)

func main() {
	n, _ := strconv.Atoi(boxfs.Env("FRAGE_RESULTS", "5"))
	c := frage.Config{
		SearxURL:  boxfs.Env("SEARX_URL", "http://127.0.0.1:8888"),
		ModelURL:  boxfs.Env("MODEL_URL", ""),
		ModelKey:  os.Getenv("MODEL_KEY"),
		ModelName: boxfs.Env("MODEL_NAME", "gemma-4-e4b"),
		Language:  boxfs.Env("FRAGE_LANGUAGE", "de"),
		Results:   n,
	}
	if c.ModelURL == "" {
		log.Fatal("frage: MODEL_URL is required")
	}
	addr := boxfs.Env("FRAGE_LISTEN", "0.0.0.0:8710")
	log.Printf("frage: listening on %s, searx=%s, model=%s", addr, c.SearxURL, c.ModelName)
	srv := &http.Server{Addr: addr, Handler: frage.Handler(c), ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
