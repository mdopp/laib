// Command laib-operator is the box's admin agent (ADR 0013), its own
// program with its own line budget (ADR 0023). It wakes on
// Laib's webhook, on a tick and at the report hour, and runs one session
// per wake-up through the operator package. Configuration is environment
// only; the unit is rendered by laib from box.yml operator.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mdopp/laib/operator"
)

func main() {
	once := flag.String("once", "", "run one session for this trigger kind (tick|report|event:<service>) and exit")
	flag.Parse()

	op := &operator.Operator{
		MCP:  &operator.MCP{URL: env("LAIB_MCP_URL", "http://127.0.0.1:5900/mcp"), Token: os.Getenv("LAIB_TOKEN")},
		LLM:  &operator.LLM{URL: strings.TrimRight(env("MODEL_URL", ""), "/"), Key: os.Getenv("MODEL_KEY"), Model: env("MODEL_NAME", "gemma-4-e4b")},
		Home: env("OPERATOR_HOME", "/mnt/data/laib/operator"),
	}
	if op.MCP.Token == "" || op.LLM.URL == "" {
		log.Fatal("laib-operator: LAIB_TOKEN and MODEL_URL are required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// The typed surface (#53) is the default: a household model gets a menu
	// per trigger with typed parameters; OPERATOR_INTERFACE=cli gives a large
	// model the verb table. The menu's service enum comes from laib status.
	op.Typed = env("OPERATOR_INTERFACE", "typed") != "cli"
	if op.Typed {
		if text, _, err := op.MCP.Call(ctx, "laib_status", map[string]string{"args": ""}); err == nil {
			op.Services = operator.ServicesFromStatus(text)
		}
	}

	if *once != "" {
		tr := operator.Trigger{Kind: *once}
		if strings.HasPrefix(*once, "event:") {
			tr = operator.Trigger{Kind: "event", Service: strings.TrimPrefix(*once, "event:"), Detail: "manual"}
		}
		s, err := op.Run(ctx, tr)
		b, _ := json.MarshalIndent(s, "", "  ")
		fmt.Println(string(b))
		if err != nil {
			log.Fatal(err)
		}
		return
	}

	var mu sync.Mutex // one session at a time; the envelope's budget is per hour anyway
	run := func(tr operator.Trigger) {
		mu.Lock()
		defer mu.Unlock()
		s, err := op.Run(ctx, tr)
		if err != nil {
			log.Printf("session %s/%s: %v", tr.Kind, tr.Service, err)
			return
		}
		log.Printf("session %s/%s: %s, %d calls, %dms", tr.Kind, tr.Service, s.Outcome, len(s.Calls), s.Duration)
	}

	// Webhook: Laib posts journal entries (red results, proposals, readiness).
	// Reds start an incident session per service, debounced for ten minutes.
	last := map[string]time.Time{}
	var lastMu sync.Mutex
	mux := http.NewServeMux()
	mux.HandleFunc("POST /event", func(w http.ResponseWriter, r *http.Request) {
		var e struct {
			Verb    string `json:"verb"`
			Service string `json:"service"`
			Result  string `json:"result"`
			Detail  string `json:"detail"`
			Actor   string `json:"actor"`
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err := json.Unmarshal(body, &e); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		if e.Result != "red" || e.Actor == "ops" || e.Verb == "escalate" || e.Verb == "notify" {
			return // our own reds and escalations do not re-trigger us
		}
		key := e.Service
		if key == "" {
			key = e.Verb
		}
		lastMu.Lock()
		recent := time.Since(last[key]) < 10*time.Minute
		if !recent {
			last[key] = time.Now()
		}
		lastMu.Unlock()
		if recent {
			return
		}
		go run(operator.Trigger{Kind: "event", Service: e.Service, Detail: e.Verb + ": " + e.Detail})
	})
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"operator":"bereit"}`)
	})
	srv := &http.Server{Addr: env("OPERATOR_LISTEN", "127.0.0.1:5901"), Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	tick, _ := time.ParseDuration(env("OPERATOR_TICK", "10m"))
	if tick <= 0 {
		tick = 10 * time.Minute
	}
	ticker := time.NewTicker(tick)
	reportAt := env("OPERATOR_REPORT_AT", "07:00")
	reportedDay := ""
	log.Printf("laib-operator: mcp=%s model=%s tick=%s report=%s", op.MCP.URL, op.LLM.Model, tick, reportAt)
	for {
		select {
		case <-ctx.Done():
			srv.Shutdown(context.Background())
			return
		case now := <-ticker.C:
			if now.Format("15:04") >= reportAt && reportedDay != now.Format("2006-01-02") {
				reportedDay = now.Format("2006-01-02")
				run(operator.Trigger{Kind: "report"})
				continue
			}
			run(operator.Trigger{Kind: "tick"})
		}
	}
}

// env is an environment variable or its default. The Operator imports
// nothing of the kernel (ADR 0023); this is boxfs.Env's one line.
func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
