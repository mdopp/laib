// Command laib-lease is the llama template's sidecar: policy proxy on PORT
// in front of the router, plus the GPU lease API (ADR 0011). It runs in its
// container (ADR 0027): its lease folder and presets.json are mounted, and
// it pauses services through laib's MCP (LAIB_MCP_URL) with the lease
// hand's token (LAIB_TOKEN), never with systemctl.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/lease"
)

func main() {
	var cfg lease.Config
	b, err := os.ReadFile(boxfs.Env("PRESETS_FILE", "/presets.json"))
	if err != nil {
		log.Fatal("laib-lease: ", err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		log.Fatal("laib-lease: presets.json: ", err)
	}
	cfg.RouterURL = boxfs.Env("ROUTER_URL", "http://127.0.0.1:11434")
	cfg.EmbedURL = os.Getenv("EMBED_URL") // llama-embed (#567)
	cfg.LeaseDir = boxfs.Env("LEASE_DIR", "/lease")
	cfg.Token = os.Getenv("LEASE_TOKEN")
	if d, err := time.ParseDuration(boxfs.Env("LEASE_RENEW", "10m")); err == nil {
		cfg.Renew = d
	}
	if d, err := time.ParseDuration(boxfs.Env("LEASE_WARN_AFTER", "20m")); err == nil {
		cfg.WarnAfter = d
	}
	// modes name roles and containers; laib status names the units (#42)
	cfg.Laib = lease.MCP{URL: boxfs.Env("LAIB_MCP_URL", "http://127.0.0.1:5900/mcp"), Token: os.Getenv("LAIB_TOKEN")}
	s, err := lease.New(cfg)
	if err != nil {
		log.Fatal("laib-lease: ", err)
	}
	addr := boxfs.Env("LISTEN", "0.0.0.0:11435")
	log.Printf("laib-lease: %s → %s, household %s, modes %d", addr, cfg.RouterURL, cfg.Household, len(cfg.Modes))
	srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
