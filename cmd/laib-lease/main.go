// Command laib-lease is the llama template's sidecar: policy proxy on PORT
// in front of the router, plus the GPU lease API (ADR 0011).
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/lease"
	"github.com/mdopp/laib/internal/state"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	var cfg lease.Config
	b, err := os.ReadFile(env("PRESETS_FILE", "/lease/presets.json"))
	if err != nil {
		log.Fatal("laib-lease: ", err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		log.Fatal("laib-lease: presets.json: ", err)
	}
	cfg.RouterURL = env("ROUTER_URL", "http://127.0.0.1:11434")
	cfg.LeaseDir = env("LEASE_DIR", "/lease")
	if d, err := time.ParseDuration(env("AUTO_IDLE", "30m")); err == nil {
		cfg.AutoIdle = d
	}
	cfg.Token = os.Getenv("LEASE_TOKEN")
	if d, err := time.ParseDuration(env("LEASE_RENEW", "10m")); err == nil {
		cfg.Renew = d
	}
	if d, err := time.ParseDuration(env("LEASE_WARN_AFTER", "20m")); err == nil {
		cfg.WarnAfter = d
	}
	// modes name roles and containers; the units come from laib's state (#42)
	cfg.Paths = boxfs.Default()
	st, err := state.Load(cfg.Paths.State)
	if err != nil {
		log.Fatal("laib-lease: state: ", err)
	}
	cfg.Units = func(role string) (string, []string) {
		for _, name := range st.ServiceNames() {
			for _, p := range st.Services[name].Contract.Provides {
				if p == role {
					return name, st.Services[name].UnitNames()
				}
			}
		}
		return "", nil
	}
	s, err := lease.New(cfg)
	if err != nil {
		log.Fatal("laib-lease: ", err)
	}
	addr := env("LISTEN", "0.0.0.0:11435")
	log.Printf("laib-lease: %s → %s, household %s, modes %d", addr, cfg.RouterURL, cfg.Household, len(cfg.Modes))
	srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
