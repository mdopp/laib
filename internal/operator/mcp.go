// Package operator is the box's admin agent (ADR 0013): it observes through
// the Laib MCP as the ops hand, thinks with an OpenAI-compatible model, and
// acts through the same MCP. Every side effect is a Laib verb, so the
// envelope, the journal and the budget apply.
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

// MCP is a minimal JSON-RPC client for laib serve's /mcp.
type MCP struct {
	URL    string
	Token  string
	Client *http.Client
	next   int
}

// Tool is one MCP tool as advertised.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func (m *MCP) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	m.next++
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": m.next, "method": method, "params": params})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.Token)
	client := m.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Minute}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
		return nil, fmt.Errorf("mcp %s: %s %s", method, resp.Status, string(b))
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, err
	}
	if env.Error != nil {
		return nil, fmt.Errorf("mcp %s: %d %s", method, env.Error.Code, env.Error.Message)
	}
	return env.Result, nil
}

// Tools lists what the hand may call.
func (m *MCP) Tools(ctx context.Context) ([]Tool, error) {
	if _, err := m.call(ctx, "initialize", map[string]any{}); err != nil {
		return nil, err
	}
	raw, err := m.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var out struct {
		Tools []Tool `json:"tools"`
	}
	return out.Tools, json.Unmarshal(raw, &out)
}

// Call runs one tool and returns its text output and whether it errored.
func (m *MCP) Call(ctx context.Context, name string, args map[string]string) (string, bool, error) {
	raw, err := m.call(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return "", true, err
	}
	var out struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", true, err
	}
	text := ""
	for _, c := range out.Content {
		text += c.Text
	}
	return text, out.IsError, nil
}
