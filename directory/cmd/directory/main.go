// Command directory is the Bote's directory (ADR 0030) as an MCP tool on
// loopback inside the bote pod, in the assistant's name (ASSISTANT, box.yml
// assistant). It reads nothing else from the box.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mdopp/laib/directory"
)

var Version = "dev"

func main() {
	caps := filepath.Join(env("DIRECTORY_DATA", "/data"), "faehigkeiten.txt") // the Bote's tools, written by post-deploy
	who := directory.Who{Name: env("ASSISTANT", "Homes")}
	if n := os.Getenv("ADMIN_NUMBER"); n != "" {
		who.Admins = []string{n} // only the admin is offered the box (#863)
	}
	if acct := os.Getenv("SIGNAL_ACCOUNT"); acct != "" && len(who.Admins) > 0 {
		who.Resolve = directory.SignalContacts(env("SIGNAL_URL", "http://127.0.0.1:8080"), acct, nil) // signal-cli in the pod (#127)
	}
	t := directory.New(func() []string {
		b, _ := os.ReadFile(caps)
		var out []string
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				out = append(out, l)
			}
		}
		return out
	}, who)
	addr := env("DIRECTORY_LISTEN", "127.0.0.1:8644")
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", directory.Server(t, Version).Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	log.Printf("directory %s: listening on %s as %s", Version, addr, who.Name)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
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
