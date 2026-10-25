package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/token"
)

// MCP over streamable HTTP, JSON-RPC 2.0, stdlib only. The tool list is the
// verb table filtered by the hand's ceiling (docs/INTERFACE.md); a call runs
// the verb in-process with --json, writing to its own buffers. Read verbs
// share Mu; anything that changes the box takes it alone (#289), on top
// of the apply lock the mutating verbs hold.
var Mu sync.RWMutex

// Allowed are the verbs an agent may reach at all. serve, init and token are
// the box's own business; secret set would put a value into a transcript.
var Allowed = map[string]bool{
	"status": true, "inbox": true, "plan": true, "apply": true, "journal": true, "context": true, "logs": true, "note": true, "inspect": true, "read": true, "look": true, "write": true, "exec": true, "restart": true, "start": true, "stop": true, "backups": true, "escalate": true,
	"template": true, "propose": true, "approve": true, "reject": true, "rollback": true, "converge": true, "backup": true, "restore": true, "adopt": true, "ignore": true, "version": true, "diag": true, "wish": true, "repair": true, "skill": true, "updates": true, "net": true,
	"person_invite": true, "person_pending": true, "person_signal": true, "session": true, "calendar": true, "zuhause": true, "scan": true, "state": true, "pause": true, "resume": true,
}

// DoorHands are the hands of programs behind the verb door, with the
// ceiling their calls back run under whatever they send (ADR 0027):
// laib-haushalt proposes, and the dial applies nothing it proposes.
var DoorHands = map[string]string{"haushalt": "propose"}

type ceilingKey struct{}

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

// Handler authenticates the bearer, then dispatches.
func Handler(env *cli.Env) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		store, err := token.Load(cli.TokenPath(env))
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
		st, err := cli.LoadState(env)
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
		// a call back from behind the verb door (ADR 0026, 0027) runs under its
		// ceiling: the header can only lower it, a door hand is held to its own
		ceiling := r.Header.Get("X-Laib-Ceiling")
		if ceiling != "" && policy.Index(ceiling) < 0 {
			ceiling = "read"
		}
		if c, ok := DoorHands[hand]; ok && (ceiling == "" || policy.Index(ceiling) > policy.Index(c)) {
			ceiling = c
		}
		ctx := context.WithValue(r.Context(), ceilingKey{}, ceiling)
		// a call in a skill's name (ADR 0023): only the skill's granted tools
		inSkill := r.Header.Get("X-Laib-Skill")
		switch req.Method {
		case "initialize":
			rpcReply(w, req.ID, map[string]any{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "laib", "version": cli.Version},
				"instructions":    "Laib is the control plane of this box. Your hand is " + hand + " (ceiling " + st.Policy.Hands[hand].Ceiling + "). Run laib_context first. Changes below your ceiling become proposals a human approves.",
			}, nil)
		case "notifications/initialized", "ping":
			rpcReply(w, req.ID, map[string]any{}, nil)
		case "tools/list":
			tools := Tools(st, hand)
			if inSkill != "" {
				tools = SkillTools(env, st, hand, inSkill, tools)
			}
			rpcReply(w, req.ID, map[string]any{"tools": tools}, nil)
		case "tools/call":
			var p struct {
				Name string         `json:"name"`
				Args map[string]any `json:"arguments"`
			}
			json.Unmarshal(req.Params, &p)
			if inSkill != "" {
				if why := SkillGate(env, st, hand, inSkill, strings.TrimPrefix(p.Name, "laib_")); why != "" {
					rpcReply(w, req.ID, map[string]any{"content": []map[string]string{{"type": "text", "text": "nicht ausgeführt: " + why}}, "isError": true}, nil)
					return
				}
			}
			out, code := Call(ctx, env, st, hand, p.Name, p.Args)
			rpcReply(w, req.ID, map[string]any{"content": []map[string]string{{"type": "text", "text": out}}, "isError": code != cli.ExitOK}, nil)
		default:
			rpcReply(w, req.ID, nil, &rpcError{-32601, "method not found: " + req.Method})
		}
	}
}

// Tools derives the tool list from the verb table.
func Tools(st *state.State, hand string) []map[string]any {
	var tools []map[string]any
	for _, v := range Verbs {
		if !Allowed[v.Name] || !policy.Allowed(st.Policy, hand, v.Tier) || !policy.MayVerb(st.Policy, hand, v.Name) {
			continue
		}
		tools = append(tools, map[string]any{
			"name":        "laib_" + v.Name,
			"description": v.Help + " (tier " + v.Tier + ")",
			"inputSchema": mcpSchema(st, v.Name),
		})
	}
	return tools
}

func InputSchema(verb string) map[string]any {
	props := map[string]any{
		"args": map[string]any{"type": "string", "description": "arguments exactly as on the command line, e.g. \"media --since 2h\" or \"\\\"reason text\\\"\""},
	}
	if verb == "write" {
		props["stdin"] = map[string]any{"type": "string", "description": "the file content"}
	}
	if verb == "propose" {
		props["args"] = map[string]any{"type": "string", "description": "the reason in quotes, plus --set services/<svc>/values.yml:KEY=VALUE (repeatable), --path box.yml:a.b.c=VALUE, --add <template> [--name <svc>] [KEY=VALUE…], or --patch together with the patch property"}
		props["patch"] = map[string]any{"type": "string", "description": "a unified diff against the state repo root (paths like services/x/values.yml); applied in a worktree, validated, refused if the state does not load"}
	}
	return map[string]any{"type": "object", "properties": props}
}

// Call runs one verb as the hand, capturing stdout.
func Call(ctx context.Context, env *cli.Env, st *state.State, hand, name string, args map[string]any) (string, int) {
	verbName := strings.TrimPrefix(name, "laib_")
	var v *cli.Verb
	for i := range Verbs {
		if Verbs[i].Name == verbName {
			v = &Verbs[i]
		}
	}
	if v == nil || !Allowed[verbName] {
		return "unknown tool " + name, cli.ExitUsage
	}
	if !policy.Allowed(st.Policy, hand, v.Tier) {
		return fmt.Sprintf("%s may not %s (tier %s); propose instead", hand, verbName, v.Tier), cli.ExitRefused
	}
	if !policy.MayVerb(st.Policy, hand, verbName) {
		return fmt.Sprintf("%s may not %s: policy.yml limits it to %s", hand, verbName, strings.Join(st.Policy.Hands[hand].Verbs, ", ")), cli.ExitRefused
	}
	ceiling, _ := ctx.Value(ceilingKey{}).(string)
	if ceiling != "" && policy.Index(v.Tier) > policy.Index(ceiling) {
		return fmt.Sprintf("%s ist Stufe %s; ein Programm hinter der Tür darf höchstens %s", verbName, v.Tier, ceiling), cli.ExitRefused
	}
	callArgs, stdin, err := Argv(st, verbName, args)
	if err != nil {
		return "ungültige Argumente für " + name + ": " + err.Error() + " — nicht ausgeführt", cli.ExitUsage
	}
	// reads run side by side; a verb that changes something runs alone (#289);
	// a verb behind the door takes nothing: its call back does (ADR 0027)
	switch {
	case v.Door:
	case v.Tier == "read" && verbName != "skill" && verbName != "template" && verbName != "session":
		Mu.RLock()
		defer Mu.RUnlock()
	default:
		Mu.Lock()
		defer Mu.Unlock()
	}
	// the verb writes to its own buffers (#43, #289): neither the process's
	// stdout nor its stderr is ever swapped, so nothing another goroutine
	// prints can land in a reply
	var buf, errBuf bytes.Buffer
	callEnv := &cli.Env{Paths: env.Paths, JSON: true, Actor: hand, MCP: true, Out: &buf, ErrOut: &errBuf, Ceiling: ceiling}
	if stdin != "" {
		callEnv.In = strings.NewReader(stdin)
	}
	code := v.Run(ctx, callEnv, callArgs)
	out := buf.String()
	if e := strings.TrimSpace(errBuf.String()); e != "" {
		if out != "" {
			out += "\n"
		}
		out += e
	}
	return out, code
}

// SplitArgs splits a command-line string, honouring double quotes.
func SplitArgs(s string) []string {
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
