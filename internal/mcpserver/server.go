// Package mcpserver is the smallest MCP server that Hermes, Claude Code and
// laib's own client speak: JSON-RPC over POST (the streamable-HTTP
// transport's request side), initialize, tools/list, tools/call. Each tool
// is a function; the schema is given by the caller.
package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// Tool is one callable.
type Tool struct {
	Name        string
	Description string
	Schema      map[string]any // JSON schema of the arguments
	Call        func(ctx context.Context, args map[string]any) (string, error)
}

// Server describes the MCP endpoint.
type Server struct {
	Name         string
	Version      string
	Instructions string
	Token        string // optional bearer; empty = no auth (loopback inside a pod)
	Tools        []Tool
}

type request struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func reply(w http.ResponseWriter, id json.RawMessage, result any, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	m := map[string]any{"jsonrpc": "2.0", "id": id}
	if msg != "" {
		m["error"] = map[string]any{"code": code, "message": msg}
	} else {
		m["result"] = result
	}
	json.NewEncoder(w).Encode(m)
}

// Handler serves the endpoint.
func (s *Server) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		if s.Token != "" && strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") != s.Token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var req request
		if err := json.Unmarshal(body, &req); err != nil {
			reply(w, nil, nil, -32700, "parse error")
			return
		}
		switch req.Method {
		case "initialize":
			reply(w, req.ID, map[string]any{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": s.Name, "version": s.Version},
				"instructions":    s.Instructions,
			}, 0, "")
		case "notifications/initialized", "ping":
			reply(w, req.ID, map[string]any{}, 0, "")
		case "tools/list":
			var tools []map[string]any
			for _, t := range s.Tools {
				tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.Schema})
			}
			reply(w, req.ID, map[string]any{"tools": tools}, 0, "")
		case "tools/call":
			var p struct {
				Name string         `json:"name"`
				Args map[string]any `json:"arguments"`
			}
			json.Unmarshal(req.Params, &p)
			for _, t := range s.Tools {
				if t.Name != p.Name {
					continue
				}
				out, err := t.Call(r.Context(), p.Args)
				isErr := err != nil
				if isErr {
					out = err.Error()
				}
				reply(w, req.ID, map[string]any{"content": []map[string]string{{"type": "text", "text": out}}, "isError": isErr}, 0, "")
				return
			}
			reply(w, req.ID, nil, -32602, "unknown tool: "+p.Name)
		default:
			reply(w, req.ID, nil, -32601, "method not found: "+req.Method)
		}
	}
}

// Str reads a string argument.
func Str(args map[string]any, k string) string {
	v, _ := args[k].(string)
	return strings.TrimSpace(v)
}
