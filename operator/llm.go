package operator

import (
	"bufio"
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
)

// LLM talks to an OpenAI-compatible /v1/chat/completions with tools.
type LLM struct {
	URL       string // …/v1
	Key       string
	Model     string
	Client    *http.Client
	MaxTokens int           // per reply, thinking included; 0: 8192 (#489)
	Timeout   time.Duration // per reply; 0: 5 minutes
}

// Message is one chat message; tool calls and tool results included.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
	in, out    int        // the reply's tokens, prompt and completion, as the server counted them (#859)
}

// usage is what an OpenAI-compatible server says a reply cost.
type usage struct {
	In  int `json:"prompt_tokens"`
	Out int `json:"completion_tokens"`
}

// ToolCall is the model asking for a tool.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// ToolDef is the OpenAI function schema derived from an MCP tool.
type ToolDef struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

// Defs converts MCP tools into OpenAI tool definitions.
func Defs(tools []Tool) []ToolDef {
	var out []ToolDef
	for _, t := range tools {
		var d ToolDef
		d.Type = "function"
		d.Function.Name = t.Name
		d.Function.Description = t.Description
		d.Function.Parameters = t.InputSchema
		if d.Function.Parameters == nil {
			d.Function.Parameters = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, d)
	}
	return out
}

// ContextWindow asks llama-server's /props (through the lease proxy, for
// this model) how many tokens the model's context holds (#285).
func (l *LLM) ContextWindow(ctx context.Context) (int, error) {
	base := strings.TrimSuffix(strings.TrimRight(l.URL, "/"), "/v1")
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/props?model="+url.QueryEscape(l.Model), nil)
	if err != nil {
		return 0, err
	}
	if l.Key != "" {
		r.Header.Set("Authorization", "Bearer "+l.Key)
	}
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(r)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	var p struct {
		Settings struct {
			NCtx int `json:"n_ctx"`
		} `json:"default_generation_settings"`
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("/props: %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil || p.Settings.NCtx <= 0 {
		return 0, fmt.Errorf("/props: no n_ctx (%v)", err)
	}
	return p.Settings.NCtx, nil
}

// personQuiet is how long after a person's model request no session
// starts: the Bote's next turn usually comes within it (#550).
const personQuiet = 2 * time.Minute

// Busy says why the model is not to be asked now, or "" (#550): an
// Operator session evicted the Bote's prompt cache and ran into the
// household model's cold start. Not while the lease is not haushalt, the
// model is loading, the router reports it not loaded, or a person asked
// the model less than personQuiet ago (the proxy's last_human_at). A model
// without the lease proxy in front (no /api/model-lease) is never busy.
// With cold an unloaded model is no reason (#792): the router unloads it
// when idle, and an incident that waited for someone else to warm it
// waited all day.
func (l *LLM) Busy(ctx context.Context, cold bool) string {
	base := strings.TrimSuffix(strings.TrimRight(l.URL, "/"), "/v1")
	client := l.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	get := func(path string, v any) bool {
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
		if err != nil {
			return false
		}
		resp, err := client.Do(r)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(v) == nil
	}
	var lease struct {
		State, Mode, Holder string
		LastHuman           int64 `json:"last_human_at"`
	}
	if !get("/api/model-lease", &lease) {
		return ""
	}
	switch {
	case lease.Mode != "" && lease.Mode != "haushalt":
		return fmt.Sprintf("die Grafikkarte ist vergeben: %s hält sie im Modus %s", cmp.Or(lease.Holder, "jemand"), lease.Mode)
	case lease.State == "preparing":
		return "das Modell lädt gerade"
	case lease.LastHuman != 0 && time.Since(time.Unix(lease.LastHuman, 0)) < personQuiet:
		ago := time.Since(time.Unix(lease.LastHuman, 0)).Round(time.Second)
		return fmt.Sprintf("eine Person hat das Modell vor %s gefragt; die Sitzung verdrängte ihren Prompt-Cache", ago)
	}
	var models struct {
		Data []struct {
			ID     string
			Status struct{ Value string }
		}
	}
	if !cold {
		get("/v1/models", &models)
	}
	for _, m := range models.Data {
		if m.ID == l.Model && m.Status.Value != "" && m.Status.Value != "loaded" {
			return "das Modell " + l.Model + " ist nicht geladen (" + m.Status.Value + "); die Sitzung wäre sein Kaltstart"
		}
	}
	return ""
}

// Chat sends the conversation and returns the assistant message. It
// streams and carries max_tokens (#489): a model caught in a thinking loop
// stops at the budget, and on the timeout the connection closes, which a
// streaming llama-server notices and frees the slot — a non-streaming one
// generated on to the window's end.
func (l *LLM) Chat(ctx context.Context, msgs []Message, tools []ToolDef) (Message, error) {
	budget, wait := cmp.Or(l.MaxTokens, 8192), cmp.Or(l.Timeout, 5*time.Minute)
	req := map[string]any{"model": l.Model, "messages": msgs, "temperature": 0.2, "max_tokens": budget, "stream": true,
		"stream_options": map[string]any{"include_usage": true}} // the tokens, counted (#859)
	if len(tools) > 0 {
		req["tools"] = tools
		req["tool_choice"] = "auto"
	}
	body, _ := json.Marshal(req)
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, l.URL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Message{}, err
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Laib-Background", "operator") // not a person's request (#550)
	if l.Key != "" {
		r.Header.Set("Authorization", "Bearer "+l.Key)
	}
	client := l.Client
	if client == nil {
		client = &http.Client{}
	}
	resp, err := client.Do(r)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
		return Message{}, fmt.Errorf("model: %s %s", resp.Status, string(b))
	}
	var m Message
	var finish string
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		m, finish, err = readStream(resp.Body)
	} else {
		var out struct {
			Choices []struct {
				Message      Message `json:"message"`
				FinishReason string  `json:"finish_reason"`
			} `json:"choices"`
			Usage usage `json:"usage"`
		}
		err = json.NewDecoder(resp.Body).Decode(&out)
		if err == nil && len(out.Choices) == 0 {
			err = fmt.Errorf("model: no choices")
		}
		if err == nil {
			m, finish = out.Choices[0].Message, out.Choices[0].FinishReason
			m.in, m.out = out.Usage.In, out.Usage.Out
		}
	}
	if err != nil {
		return Message{}, err
	}
	if finish == "length" {
		return Message{}, fmt.Errorf("model: reply cut at max_tokens (%d): a thinking or repeating loop", budget)
	}
	// a reasoning model's <think> block is not the answer (#285)
	for {
		i, j := strings.Index(m.Content, "<think>"), strings.Index(m.Content, "</think>")
		if i < 0 || j < i {
			break
		}
		m.Content = strings.TrimSpace(m.Content[:i] + m.Content[j+len("</think>"):])
	}
	return m, nil
}

// readStream assembles an OpenAI server-sent-event stream into one message:
// content in order, tool calls by index with their arguments joined.
func readStream(body io.Reader) (Message, string, error) {
	m := Message{Role: "assistant"}
	var content strings.Builder
	finish := ""
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data:")
		data = strings.TrimSpace(data)
		if !ok || data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}
		var ev struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *usage `json:"usage"` // the last chunk's, with include_usage
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return Message{}, "", fmt.Errorf("model stream: %w", err)
		}
		if ev.Usage != nil {
			m.in, m.out = ev.Usage.In, ev.Usage.Out
		}
		for _, c := range ev.Choices {
			content.WriteString(c.Delta.Content)
			for _, d := range c.Delta.ToolCalls {
				for len(m.ToolCalls) <= d.Index {
					m.ToolCalls = append(m.ToolCalls, ToolCall{Type: "function"})
				}
				tc := &m.ToolCalls[d.Index]
				tc.ID = cmp.Or(d.ID, tc.ID)
				tc.Type = cmp.Or(d.Type, tc.Type)
				tc.Function.Name += d.Function.Name
				tc.Function.Arguments += d.Function.Arguments
			}
			finish = cmp.Or(c.FinishReason, finish)
		}
	}
	m.Content = content.String()
	return m, finish, sc.Err()
}
