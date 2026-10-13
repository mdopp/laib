// Command persona is the Bote's persona engine (ADR 0020) as an MCP tool on
// loopback inside the bote pod; today it speaks as Joshua. It reads nothing
// from the box.
package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/persona"
)

var Version = "dev"

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	caps := filepath.Join(env("PERSONA_DATA", "/data"), "faehigkeiten.txt") // the Bote's tools, written by post-deploy
	t := persona.New(func() []string {
		b, _ := os.ReadFile(caps)
		var out []string
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				out = append(out, l)
			}
		}
		return out
	})
	addr := env("PERSONA_LISTEN", "127.0.0.1:8644")
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", persona.Server(t, Version).Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	log.Printf("persona %s: listening on %s", Version, addr)
	log.Fatal((&http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}).ListenAndServe())
}
