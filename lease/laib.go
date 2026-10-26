package lease

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
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
	for i, a := range args { // one word each: the reason has blanks, never a quote
		args[i] = `"` + strings.ReplaceAll(a, `"`, "") + `"`
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "laib_" + tool, "arguments": map[string]string{"args": strings.Join(args, " ")}}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.URL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+m.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var env struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", fmt.Errorf("laib: %s", strings.TrimSpace(string(raw)))
	}
	if env.Error != nil {
		return "", errors.New("laib: " + env.Error.Message)
	}
	text := ""
	if len(env.Result.Content) > 0 {
		text = strings.TrimSpace(env.Result.Content[0].Text)
	}
	if env.Result.IsError {
		return "", errors.New(text)
	}
	return text, nil
}

// writeAtomic is the kernel's boxfs.WriteAtomic (#148), which this program
// may not import: the temp file synced before the rename, the directory
// after, so a power cut leaves the old lease or the new one.
func writeAtomic(tmp, path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
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
