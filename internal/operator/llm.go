package operator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// LLM talks to an OpenAI-compatible /v1/chat/completions with tools.
type LLM struct {
	URL    string // …/v1
	Key    string
	Model  string
	Client *http.Client
}

// Message is one chat message; tool calls and tool results included.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
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

// Chat sends the conversation and returns the assistant message.
func (l *LLM) Chat(ctx context.Context, msgs []Message, tools []ToolDef) (Message, error) {
	req := map[string]any{"model": l.Model, "messages": msgs, "temperature": 0.2}
	if len(tools) > 0 {
		req["tools"] = tools
		req["tool_choice"] = "auto"
	}
	body, _ := json.Marshal(req)
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, l.URL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Message{}, err
	}
	r.Header.Set("Content-Type", "application/json")
	if l.Key != "" {
		r.Header.Set("Authorization", "Bearer "+l.Key)
	}
	client := l.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
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
	var out struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Message{}, err
	}
	if len(out.Choices) == 0 {
		return Message{}, fmt.Errorf("model: no choices")
	}
	return out.Choices[0].Message, nil
}
