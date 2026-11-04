package lease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mdopp/laib/mcpclient"
)

// MCP is laib serve as the lease reaches it from its container (ADR 0027):
// laib's MCP with the lease hand's token — status, pause, resume, note.
// The client is the program's own (#677): it imports nothing of the kernel.
type MCP struct {
	URL, Token string
	H          *http.Client
}

func (m MCP) ask(ctx context.Context, tool string, args ...string) (string, error) {
	h := m.H
	if h == nil {
		h = &http.Client{Timeout: time.Minute}
	}
	a, err := mcpclient.Post(ctx, h, m.URL, m.Token, nil, "tools/call",
		map[string]any{"name": "laib_" + tool, "arguments": mcpclient.Args(args...)})
	if err != nil {
		return "", err
	}
	if a.Bad != nil {
		return "", fmt.Errorf("laib: %s", strings.TrimSpace(string(a.Body)))
	}
	if a.Error != nil {
		return "", errors.New("laib: " + a.Error.Message)
	}
	text, isError, _ := a.Tool()
	if text = strings.TrimSpace(text); isError {
		return "", errors.New(text)
	}
	return text, nil
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
