// Package mcpclient is the smallest client of laib serve's MCP, which the
// programs that may not import the kernel share (laib-haushalt, laib-lease,
// laib-notify, the Operator): one JSON-RPC request over POST with a hand's
// bearer, one answer. Each program keeps its own verbs and says its own
// errors; it imports nothing of laib.
package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// Answer is laib serve's reply to one request, read whole.
type Answer struct {
	Status     string // "200 OK"
	StatusCode int
	Body       []byte
	Result     json.RawMessage
	Error      *Error // a JSON-RPC error
	Bad        error  // the body is no JSON-RPC answer: the decoder's error
}

// Error is a JSON-RPC error.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Post sends method with params as the hand of token, with the extra
// headers (X-Laib-Ceiling, X-Laib-Skill). Its error is the transport's;
// what laib answered, also an error, is in the Answer.
func Post(ctx context.Context, h *http.Client, url, token string, header map[string]string, method string, params any) (*Answer, error) {
	// one request, one answer over HTTP: the id needs no counter
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := h.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	a := &Answer{Status: resp.Status, StatusCode: resp.StatusCode}
	if a.Body, err = io.ReadAll(io.LimitReader(resp.Body, 16<<20)); err != nil {
		return nil, err
	}
	a.Bad = json.NewDecoder(bytes.NewReader(a.Body)).Decode(&struct {
		Result *json.RawMessage `json:"result"`
		Error  **Error          `json:"error"`
	}{&a.Result, &a.Error})
	return a, nil
}

// Tool is a tools/call result: its text, the parts joined, whether the
// tool said it failed, and whether it had any text part at all.
func (a *Answer) Tool() (text string, isError, ok bool) {
	var r struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	json.Unmarshal(a.Result, &r)
	for _, c := range r.Content {
		text += c.Text
	}
	return text, r.IsError, len(r.Content) > 0
}

// Args is a laib verb's words as the one args string its tool takes, each
// word quoted (a word has blanks, never a quote).
func Args(words ...string) map[string]string {
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = `"` + strings.ReplaceAll(w, `"`, "") + `"`
	}
	return map[string]string{"args": strings.Join(quoted, " ")}
}
