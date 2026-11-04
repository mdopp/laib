// Package operator is the box's admin agent (ADR 0013): it observes through
// the Laib MCP as the ops hand, thinks with an OpenAI-compatible model, and
// acts through the same MCP. Every side effect is a Laib verb, so the
// envelope, the journal and the budget apply.
package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/mdopp/laib/mcpclient"
)

// MCP is a minimal JSON-RPC client for laib serve's /mcp.
type MCP struct {
	URL    string
	Token  string
	Skill  string // when set, every call is made in this skill's name (X-Laib-Skill, ADR 0023)
	Client *http.Client
}

// Tool is one MCP tool as advertised.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func (m *MCP) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	client := m.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Minute}
	}
	var header map[string]string
	if m.Skill != "" {
		header = map[string]string{"X-Laib-Skill": m.Skill}
	}
	a, err := mcpclient.Post(ctx, client, m.URL, m.Token, header, method, params)
	if err != nil {
		return nil, err
	}
	if a.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mcp %s: %s %s", method, a.Status, string(a.Body[:min(len(a.Body), 2000)]))
	}
	if a.Bad != nil {
		return nil, a.Bad
	}
	if a.Error != nil {
		return nil, fmt.Errorf("mcp %s: %d %s", method, a.Error.Code, a.Error.Message)
	}
	return a.Result, nil
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
func (m *MCP) Call(ctx context.Context, name string, args map[string]any) (string, bool, error) {
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
