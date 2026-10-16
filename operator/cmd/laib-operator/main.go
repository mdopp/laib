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
	once := flag.String("once", "", "run one skill now (its name, or event:<service> for the event skills) and exit")
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
	// per skill with typed parameters; OPERATOR_INTERFACE=cli gives a large
	// model the verb table. The menu's service enum comes from laib status.
	op.Typed = env("OPERATOR_INTERFACE", "typed") != "cli"
	if op.Typed {
		if text, _, err := op.MCP.Call(ctx, "laib_status", map[string]string{"args": ""}); err == nil {
			op.Services = operator.ServicesFromStatus(text)
		}
	}

	runner := &operator.Runner{Op: op, Tick: env("OPERATOR_TICK", "10m"), ReportAt: env("OPERATOR_REPORT_AT", "07:00")}
	runner.LoadLast()
	// the skills come from the kernel (ADR 0023): which exist, which rest,
	// what each may call; the last good list stands while laib is away
	var skills []operator.Skill
	var skillsMu sync.Mutex
	current := func() []operator.Skill {
		skillsMu.Lock()
		defer skillsMu.Unlock()
		if sks, err := op.MCP.Skills(ctx); err == nil {
			skills = sks
		} else {
			log.Printf("skills: %v", err)
		}
		return skills
	}
	logged := func(ss []operator.Session) {
		for _, s := range ss {
			log.Printf("session %s/%s: %s, %d calls, %dms", s.Skill, s.Trigger.Service, s.Outcome, len(s.Calls), s.Duration)
		}
	}

	if *once != "" {
		// -once <skill> runs that skill now; -once event:<service> runs the event skills
		var ss []operator.Session
		if svc, ok := strings.CutPrefix(*once, "event:"); ok {
			ss = runner.Event(ctx, current(), operator.Trigger{Kind: "event", Service: svc, Detail: "manual"})
		} else {
			for _, sk := range current() {
				if sk.Name == *once && sk.Status == "active" {
					ss = append(ss, runner.RunNow(ctx, sk, operator.Trigger{Kind: "manual"}))
				}
			}
		}
		if len(ss) == 0 {
			log.Fatalf("laib-operator: no active skill for %q (laib skill list)", *once)
		}
		for _, s := range ss {
			b, _ := json.MarshalIndent(s, "", "  ")
			fmt.Println(string(b))
		}
		return
	}

	// Webhook: Laib posts journal entries (red results, proposals, readiness).
	// A red runs the event skills for its service, debounced for ten minutes.
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
		tr := operator.Trigger{Kind: "event", Service: e.Service, Detail: e.Verb + ": " + e.Detail}
		go func() { logged(runner.Event(ctx, current(), tr)) }()
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

	// one step a minute: every active skill whose slot has come runs
	ticker := time.NewTicker(time.Minute)
	log.Printf("laib-operator: mcp=%s model=%s tick=%s report=%s", op.MCP.URL, op.LLM.Model, runner.Tick, runner.ReportAt)
	for {
		select {
		case <-ctx.Done():
			srv.Shutdown(context.Background())
			return
		case now := <-ticker.C:
			logged(runner.Step(ctx, now, current()))
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
