package haushalt

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

// MCP is the way back to laib (ADR 0027): laib_propose over laib serve's
// MCP with the program's own token, the haushalt hand's. X-Laib-Ceiling
// carries the verb's ceiling; laib holds the hand to propose whatever it
// says, and its dial applies nothing the hand proposes.
type MCP struct {
	URL, Token string
}

// Propose files `laib propose <args>`.
func (m MCP) Propose(ctx context.Context, ceiling string, args ...string) (int, string, error) {
	var p struct {
		ID    int    `json:"id"`
		Class string `json:"class"`
	}
	err := m.call(ctx, "laib_propose", ceiling, args, &p)
	if err == nil && p.ID == 0 {
		err = errors.New("laib propose answered without a number")
	}
	return p.ID, p.Class, err
}

// Aufgabe adds `laib aufgabe <args>` and returns the task's number (#883).
func (m MCP) Aufgabe(ctx context.Context, ceiling string, args ...string) (int, error) {
	var p struct {
		Aufgabe struct {
			ID int `json:"id"`
		} `json:"aufgabe"`
	}
	err := m.call(ctx, "laib_aufgabe", ceiling, args, &p)
	return p.Aufgabe.ID, err
}

// call runs one of laib's tools with args quoted one by one (a SANE device
// name has blanks, never a quote) and decodes its answer into out.
func (m MCP) call(ctx context.Context, tool, ceiling string, args []string, out any) error {
	if m.URL == "" || m.Token == "" {
		return errors.New("laib-haushalt has no token for laib (LAIB_TOKEN): laib apply mints it")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	a, err := mcpclient.Post(ctx, http.DefaultClient, m.URL, m.Token, map[string]string{"X-Laib-Ceiling": ceiling},
		"tools/call", map[string]any{"name": tool, "arguments": mcpclient.Args(args...)})
	if err != nil {
		return fmt.Errorf("laib antwortet nicht (%s): %w", m.URL, err)
	}
	text, isError, ok := a.Tool()
	if a.Bad != nil || !ok {
		return fmt.Errorf("%s: HTTP %d, %v", tool, a.StatusCode, a.Bad)
	}
	if isError || json.Unmarshal([]byte(text), out) != nil {
		return errors.New(strings.TrimSpace(text))
	}
	return nil
}
