package haushalt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
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
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = `"` + strings.ReplaceAll(a, `"`, "") + `"`
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": map[string]string{"args": strings.Join(quoted, " ")}}})
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, m.URL, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+m.Token)
	req.Header.Set("X-Laib-Ceiling", ceiling)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("laib antwortet nicht (%s): %w", m.URL, err)
	}
	defer resp.Body.Close()
	var r struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil || len(r.Result.Content) == 0 {
		return fmt.Errorf("%s: HTTP %d, %v", tool, resp.StatusCode, err)
	}
	if text := r.Result.Content[0].Text; r.Result.IsError || json.Unmarshal([]byte(text), out) != nil {
		return errors.New(strings.TrimSpace(text))
	}
	return nil
}
