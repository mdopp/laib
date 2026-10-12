package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/token"
)

// MCP over streamable HTTP, JSON-RPC 2.0, stdlib only. The tool list is the
// verb table filtered by the hand's ceiling (docs/INTERFACE.md); a call runs
// the verb in-process with --json. One call at a time, because the verbs
// write to os.Stdout and the capture swaps it.
var mcpMu sync.Mutex

// mcpVerbs are the verbs an agent may reach at all. serve, init and token are
// the box's own business; secret set would put a value into a transcript.
var mcpVerbs = map[string]bool{
	"status": true, "inbox": true, "plan": true, "apply": true, "journal": true, "context": true, "logs": true, "note": true, "inspect": true, "read": true, "look": true, "write": true, "exec": true, "restart": true, "escalate": true,
	"template": true, "propose": true, "approve": true, "reject": true, "rollback": true, "converge": true, "backup": true, "restore": true, "adopt": true, "ignore": true, "version": true, "diag": true, "wish": true,
}

type rpcRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func rpcReply(w http.ResponseWriter, id json.RawMessage, result any, rerr *rpcError) {
	w.Header().Set("Content-Type", "application/json")
	msg := map[string]any{"jsonrpc": "2.0", "id": id}
	if rerr != nil {
		msg["error"] = rerr
	} else {
		msg["result"] = result
	}
	json.NewEncoder(w).Encode(msg)
}

// mcpHandler authenticates the bearer, then dispatches.
func mcpHandler(env *cliEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		store, err := token.Load(tokenPath(env))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		rec, ok := store.Lookup(bearer, time.Now())
		if bearer == "" || !ok {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		st, err := loadState(env)
		if err != nil {
			http.Error(w, "state: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var req rpcRequest
		if err := json.Unmarshal(body, &req); err != nil {
			rpcReply(w, nil, nil, &rpcError{-32700, "parse error"})
			return
		}
		hand := rec.Hand
		switch req.Method {
		case "initialize":
			rpcReply(w, req.ID, map[string]any{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "laib", "version": Version},
				"instructions":    "Laib is the control plane of this box. Your hand is " + hand + " (ceiling " + st.Policy.Hands[hand].Ceiling + "). Run laib_context first. Changes below your ceiling become proposals a human approves.",
			}, nil)
		case "notifications/initialized", "ping":
			rpcReply(w, req.ID, map[string]any{}, nil)
		case "tools/list":
			rpcReply(w, req.ID, map[string]any{"tools": mcpTools(st, hand)}, nil)
		case "tools/call":
			var p struct {
				Name string            `json:"name"`
				Args map[string]string `json:"arguments"`
			}
			json.Unmarshal(req.Params, &p)
			out, code := mcpCall(r.Context(), env, st, hand, p.Name, p.Args)
			rpcReply(w, req.ID, map[string]any{"content": []map[string]string{{"type": "text", "text": out}}, "isError": code != exitOK}, nil)
		default:
			rpcReply(w, req.ID, nil, &rpcError{-32601, "method not found: " + req.Method})
		}
	}
}

// mcpTools derives the tool list from the verb table.
func mcpTools(st *state.State, hand string) []map[string]any {
	var tools []map[string]any
	for _, v := range verbs {
		if !mcpVerbs[v.name] || !policy.Allowed(st.Policy, hand, v.tier) {
			continue
		}
		tools = append(tools, map[string]any{
			"name":        "laib_" + v.name,
			"description": v.help + " (tier " + v.tier + ")",
			"inputSchema": inputSchema(v.name),
		})
	}
	return tools
}

func inputSchema(verb string) map[string]any {
	props := map[string]any{
		"args": map[string]any{"type": "string", "description": "arguments exactly as on the command line, e.g. \"media --since 2h\" or \"\\\"reason text\\\"\""},
	}
	if verb == "write" {
		props["stdin"] = map[string]any{"type": "string", "description": "the file content"}
	}
	if verb == "propose" {
		props["args"] = map[string]any{"type": "string", "description": "the reason in quotes, plus --set services/<svc>/values.yml:KEY=VALUE (repeatable), --add <template> [--name <svc>] [KEY=VALUE…], or --patch together with the patch property"}
		props["patch"] = map[string]any{"type": "string", "description": "a unified diff against the state repo root (paths like services/x/values.yml); applied in a worktree, validated, refused if the state does not load"}
	}
	return map[string]any{"type": "object", "properties": props}
}

// mcpCall runs one verb as the hand, capturing stdout.
func mcpCall(ctx context.Context, env *cliEnv, st *state.State, hand, name string, args map[string]string) (string, int) {
	verbName := strings.TrimPrefix(name, "laib_")
	var v *verb
	for i := range verbs {
		if verbs[i].name == verbName {
			v = &verbs[i]
		}
	}
	if v == nil || !mcpVerbs[verbName] {
		return "unknown tool " + name, exitUsage
	}
	if !policy.Allowed(st.Policy, hand, v.tier) {
		return fmt.Sprintf("%s may not %s (tier %s); propose instead", hand, verbName, v.tier), exitRefused
	}
	mcpMu.Lock()
	defer mcpMu.Unlock()
	var buf bytes.Buffer
	callEnv := &cliEnv{paths: env.paths, json: true, actor: hand, mcp: true, out: &buf}
	callArgs := splitArgs(args["args"])
	if patch := args["patch"]; patch != "" {
		callEnv.in = strings.NewReader(patch)
		callArgs = append([]string{"--patch"}, callArgs...)
	}
	if in := args["stdin"]; in != "" {
		callEnv.in = strings.NewReader(in)
	}
	// the verb writes to buf (#43: the process stdout is never swapped, so
	// nothing another goroutine prints can land here); fail()'s one-line
	// errors still go to stderr — captured through a pipe read concurrently,
	// never a pipe nobody drains
	r, w, err := os.Pipe()
	if err != nil {
		return err.Error(), exitUsage
	}
	savedErr := os.Stderr
	os.Stderr = w
	var errBuf bytes.Buffer
	done := make(chan struct{})
	go func() { io.Copy(&errBuf, r); close(done) }()
	code := v.run(ctx, callEnv, callArgs)
	os.Stderr = savedErr
	w.Close()
	<-done
	out := buf.String()
	if e := strings.TrimSpace(errBuf.String()); e != "" {
		if out != "" {
			out += "\n"
		}
		out += e
	}
	return out, code
}

// splitArgs splits a command-line string, honouring double quotes.
func splitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	has := false
	for _, c := range s {
		switch {
		case c == '"':
			inQuote = !inQuote
			has = true
		case c == ' ' && !inQuote:
			if has {
				out = append(out, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(c)
			has = true
		}
	}
	if has {
		out = append(out, cur.String())
	}
	return out
}

var _ = proposal.Hand
