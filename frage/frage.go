// Package frage answers "was ist …": a SearXNG search, then a summary by the
// model, served both as a plain /ask and as an OpenAI-compatible chat
// endpoint so Home Assistant Assist and the Bote can use it as a tool
// (ADR 0015). No memory, no loop: one search, one summary.
package frage

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mdopp/laib/mcpserver"
)

// Config is what the service needs.
type Config struct {
	SearxURL  string // http://127.0.0.1:8888
	ModelURL  string // …/v1
	ModelKey  string
	ModelName string
	Language  string // de
	Results   int    // how many hits go into the summary
	Client    *http.Client
}

// Hit is one search result the summary cites.
type Hit struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}

// Answer is what /ask returns.
type Answer struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
	Sources  []Hit  `json:"sources"`
	MS       int64  `json:"ms"`
}

func (c Config) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{Timeout: 60 * time.Second}
}

// Search asks SearXNG for JSON results.
func (c Config) Search(ctx context.Context, q string) ([]Hit, error) {
	n := c.Results
	if n <= 0 {
		n = 5
	}
	lang := c.Language
	if lang == "" {
		lang = "de"
	}
	u := strings.TrimRight(c.SearxURL, "/") + "/search?" + url.Values{"q": {q}, "format": {"json"}, "language": {lang}, "safesearch": {"1"}}.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("suche: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("suche: %s", resp.Status)
	}
	var out struct {
		Results []Hit `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("suche: %w", err)
	}
	if len(out.Results) > n {
		out.Results = out.Results[:n]
	}
	return out.Results, nil
}

// Summarise turns the hits into a short spoken answer.
func (c Config) Summarise(ctx context.Context, q string, hits []Hit) (string, error) {
	var b strings.Builder
	for i, h := range hits {
		fmt.Fprintf(&b, "[%d] %s — %s\n%s\n\n", i+1, h.Title, h.URL, strings.TrimSpace(h.Content))
	}
	system := "Du beantwortest eine Frage aus dem Haushalt in zwei bis vier Sätzen, auf Deutsch, zum Vorlesen geeignet: keine Listen, keine Links, keine Fußnoten. Nutze nur die Treffer unten. Wenn sie die Frage nicht beantworten, sag das in einem Satz."
	body, _ := json.Marshal(map[string]any{
		"model":       c.ModelName,
		"temperature": 0.2,
		"max_tokens":  4096, // thinking included: a loop never holds a llama slot past this (#489)
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": "Frage: " + q + "\n\nTreffer:\n" + b.String()},
		},
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.ModelURL, "/")+"/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if c.ModelKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.ModelKey)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("modell: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return "", fmt.Errorf("modell: %s %s", resp.Status, t)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || len(out.Choices) == 0 {
		return "", fmt.Errorf("modell: leere Antwort")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

// Ask is search then summary.
func (c Config) Ask(ctx context.Context, q string) (Answer, error) {
	start := time.Now()
	q = strings.TrimSpace(q)
	if q == "" {
		return Answer{}, fmt.Errorf("leere Frage")
	}
	hits, err := c.Search(ctx, q)
	if err != nil {
		return Answer{}, err
	}
	if len(hits) == 0 {
		return Answer{Question: q, Answer: "Dazu habe ich nichts gefunden.", MS: time.Since(start).Milliseconds()}, nil
	}
	text, err := c.Summarise(ctx, q, hits)
	if err != nil {
		return Answer{}, err
	}
	return Answer{Question: q, Answer: text, Sources: hits, MS: time.Since(start).Milliseconds()}, nil
}

// Handler serves /ask (GET ?q= or POST {"q"}), /search (GET ?q=&lang=, the hits), /v1/chat/completions and
// Ollama's /api/chat (the last user message is the question), /v1/models,
// /api/tags, /api/version, /mcp and /health.
func Handler(c Config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, `{"frage":"bereit"}`) })
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []map[string]any{{"id": "frage", "object": "model", "owned_by": "laib"}}})
	})
	// /search: the hits alone, no summary (#544) — the Operator reads the web
	// through the box's own SearXNG; lang=all for upstream docs in English
	mux.HandleFunc("GET /search", func(w http.ResponseWriter, r *http.Request) {
		sc := c
		sc.Language, sc.Results = cmp.Or(r.URL.Query().Get("lang"), c.Language), 8
		hits, err := sc.Search(r.Context(), r.URL.Query().Get("q"))
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(append([]Hit{}, hits...))
	})
	mux.HandleFunc("/ask", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if r.Method == http.MethodPost {
			var in struct {
				Q string `json:"q"`
			}
			json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&in)
			if in.Q != "" {
				q = in.Q
			}
		}
		a, err := c.Ask(r.Context(), q)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(a)
	})
	// /mcp: Frage as a tool the Bote calls (#54) — one tool, the answer with
	// its sources, so Hermes lists it next to laib and automat.
	mux.Handle("/mcp", (&mcpserver.Server{Name: "frage", Version: "1", Instructions: "Nachschlagen im Netz: Suche plus Zusammenfassung mit Quellen.",
		Tools: []mcpserver.Tool{{
			Name:        "frage",
			Description: "Beantwortet eine Sachfrage mit einer Websuche (SearXNG) und fasst die Treffer mit Quellen zusammen.",
			Schema:      map[string]any{"type": "object", "properties": map[string]any{"frage": map[string]any{"type": "string", "description": "die Frage, z. B. „was ist ein Kolibri?“"}}, "required": []string{"frage"}},
			Call: func(ctx context.Context, args map[string]any) (string, error) {
				a, err := c.Ask(ctx, mcpserver.Str(args, "frage"))
				if err != nil {
					return "", err
				}
				var b strings.Builder
				b.WriteString(a.Answer)
				for i, h := range a.Sources {
					fmt.Fprintf(&b, "\n[%d] %s — %s", i+1, h.Title, h.URL)
				}
				return b.String(), nil
			},
		}},
	}).Handler())
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		q, system, _, ok := lastQuestion(w, r)
		if !ok {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id": "frage-" + fmt.Sprint(time.Now().UnixNano()), "object": "chat.completion", "model": "frage",
			"choices": []map[string]any{{"index": 0, "finish_reason": "stop", "message": map[string]string{"role": "assistant", "content": c.spoken(r.Context(), q, system)}}},
		})
	})
	// The Ollama API's few routes Home Assistant's Ollama integration uses
	// (#491): HA has no conversation integration for an OpenAI server with
	// its own URL, the Ollama one takes any URL — as Solaris's facade did.
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, `{"version":"frage"}`) })
	mux.HandleFunc("GET /api/tags", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"models": []map[string]any{{"name": "frage", "model": "frage", "modified_at": "2026-01-01T00:00:00Z",
			"size": 0, "digest": "frage", "details": map[string]string{"family": "frage", "format": "", "parameter_size": ""}}}})
	})
	mux.HandleFunc("POST /api/chat", func(w http.ResponseWriter, r *http.Request) {
		q, system, stream, ok := lastQuestion(w, r)
		if !ok {
			return
		}
		chunk := func(text string, done bool) map[string]any {
			m := map[string]any{"model": "frage", "created_at": time.Now().UTC().Format(time.RFC3339Nano), "message": map[string]string{"role": "assistant", "content": text}, "done": done}
			if done {
				m["done_reason"] = "stop"
			}
			return m
		}
		text := c.spoken(r.Context(), q, system)
		if !stream {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(chunk(text, true))
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		enc := json.NewEncoder(w) // one object per line
		enc.Encode(chunk(text, false))
		enc.Encode(chunk("", true))
	})
	return mux
}

// spoken is Ask's answer, or a sentence about the failure: never silence on a speaker.
// A device command HA's intents missed is answered without a search (#760).
func (c Config) spoken(ctx context.Context, q, system string) string {
	if text, ok := device(q, system); ok {
		return text
	}
	a, err := c.Ask(ctx, q)
	if err != nil {
		return "Das konnte ich gerade nicht nachschlagen: " + err.Error()
	}
	return a.Answer
}

// lastQuestion reads a chat request (OpenAI's or Ollama's): the last user
// message is the question, system HA's prompt; stream is Ollama's flag, true unless sent false.
func lastQuestion(w http.ResponseWriter, r *http.Request) (q, system string, stream, ok bool) {
	var in struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Stream *bool `json:"stream"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return "", "", false, false
	}
	for i := len(in.Messages) - 1; i >= 0; i-- {
		if in.Messages[i].Role == "user" && q == "" {
			q = in.Messages[i].Content
		}
		if in.Messages[i].Role == "system" {
			system = in.Messages[i].Content
		}
	}
	return q, system, in.Stream == nil || *in.Stream, true
}
