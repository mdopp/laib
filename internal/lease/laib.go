package lease

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/mcp"
)

// MCP is laib serve as the lease reaches it from its container (ADR 0027):
// laib's MCP with the lease hand's token — status, pause, resume, note.
type MCP struct {
	URL, Token string
	H          *http.Client
}

func (m MCP) ask(ctx context.Context, tool string, args ...string) (string, error) {
	h := m.H
	if h == nil {
		h = &http.Client{Timeout: time.Minute}
	}
	for i, a := range args { // one word each: the reason has blanks, never a quote
		args[i] = `"` + strings.ReplaceAll(a, `"`, "") + `"`
	}
	return mcp.Ask(ctx, h, m.URL, m.Token, tool, strings.Join(args, " "))
}

func (m MCP) Services(ctx context.Context) ([]Service, error) {
	text, err := m.ask(ctx, "status")
	if err != nil {
		return nil, err
	}
	var st struct {
		Services []Service `json:"services"`
	}
	return st.Services, json.Unmarshal([]byte(text), &st)
}

func (m MCP) Pause(ctx context.Context, h Hold) error {
	args := []string{h.Service, "--until", h.Until.UTC().Format(time.RFC3339)}
	for _, u := range h.Stop {
		args = append(args, "--stop", u)
	}
	for _, u := range h.Restart {
		args = append(args, "--restart", u)
	}
	_, err := m.ask(ctx, "pause", append(args, h.Reason)...)
	return err
}

func (m MCP) Resume(ctx context.Context, service string) error {
	_, err := m.ask(ctx, "resume", service)
	return err
}

func (m MCP) Note(ctx context.Context, text string) error {
	_, err := m.ask(ctx, "note", "--kind", "watch", text)
	return err
}
