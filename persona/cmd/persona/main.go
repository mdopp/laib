// Command persona is the Bote's persona engine (ADR 0020) as an MCP tool on
// loopback inside the bote pod. It speaks as the personas laib rendered to
// /personas (the house's, and one per Signal number); a persona change
// restarts it (PERSONA_REV). It reads nothing else from the box.
package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mdopp/laib/persona"
)

var Version = "dev"

func main() {
	caps := filepath.Join(env("PERSONA_DATA", "/data"), "faehigkeiten.txt") // the Bote's tools, written by post-deploy
	cast, err := persona.LoadCast(env("PERSONAS_DIR", "/personas"))
	if err != nil {
		log.Fatalf("persona: %v", err)
	}
	if n := os.Getenv("ADMIN_NUMBER"); n != "" {
		cast.Admins = []string{n} // only the admin is offered the box (#863)
	}
	if acct := os.Getenv("SIGNAL_ACCOUNT"); acct != "" && len(cast.Chat)+len(cast.Admins) > 0 {
		cast.Resolve = persona.SignalContacts(env("SIGNAL_URL", "http://127.0.0.1:8080"), acct, nil) // signal-cli in the pod (#127)
	}
	t := persona.New(func() []string {
		b, _ := os.ReadFile(caps)
		var out []string
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				out = append(out, l)
			}
		}
		return out
	}, cast)
	addr := env("PERSONA_LISTEN", "127.0.0.1:8644")
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", persona.Server(t, Version).Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	log.Printf("persona %s: listening on %s, house persona %s (%s), %d chat persona(s)", Version, addr, cast.House.Name, cast.House.Source, len(cast.Chat))
	log.Fatal((&http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}).ListenAndServe())
}

// env is the variable, or def when it is unset or empty.
func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
