// Package cli is what every verb of the laib binary shares: the
// environment a verb runs in, the exit codes, loading the state and
// recording a journal entry (#309). Verbs live in their own packages;
// cmd/laib wires them into the verb table.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/state"
)

// Env is what every verb gets.
type Env struct {
	Paths boxfs.Paths
	JSON  bool
	Actor string
	In    io.Reader // stdin for --patch; MCP passes the patch here
	MCP   bool      // the call came over MCP: no file access, so propose needs content
	Out   io.Writer // where a verb writes (#43): nil = os.Stdout; MCP and escalate pass a buffer
	// ErrOut is where a verb's errors go (#289): nil = os.Stderr; MCP passes
	// the call's own buffer, so no process-wide stderr is ever swapped
	ErrOut io.Writer
	// Ceiling is LAIB_CEILING, set by the verb door (ADR 0026): the
	// program's call back runs no verb above it, the dial applies nothing
	Ceiling string
}

// Errw is the verb's error writer.
func (e *Env) Errw() io.Writer {
	if e != nil && e.ErrOut != nil {
		return e.ErrOut
	}
	return os.Stderr
}

// Fail prints a one-line error to the verb's error writer.
func (e *Env) Fail(err error) int {
	fmt.Fprintln(e.Errw(), "laib:", strings.TrimSpace(err.Error()))
	return 1
}

// W is the verb's output: never the process stdout when a caller passed its own.
func (e *Env) W() io.Writer {
	if e != nil && e.Out != nil {
		return e.Out
	}
	return os.Stdout
}

// Stdin is the verb's input: what MCP passed, else the process stdin.
func (e *Env) Stdin() io.Reader {
	if e.In != nil {
		return e.In
	}
	return os.Stdin
}

// Emit prints JSON and returns 0.
func Emit(env *Env, v any) int {
	enc := json.NewEncoder(env.W())
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return env.Fail(err)
	}
	return 0
}

// Exit codes (docs/INTERFACE.md).
const (
	ExitOK      = 0
	ExitUsage   = 1
	ExitRed     = 2
	ExitRefused = 3
	ExitLocked  = 4
)

// LoadState loads the state repo and validates its policy.
func LoadState(env *Env) (*state.State, error) {
	st, err := state.Load(env.Paths.State)
	if err != nil {
		return nil, err
	}
	if err := policy.Validate(st.Policy); err != nil {
		return nil, err
	}
	return st, nil
}

// Record appends a journal entry and, when box.yml names a webhook, posts
// the entry there for red results and proposal events (#15). Delivery is
// best effort with one retry; a failure is its own journal line, never a
// reason to fail the verb.
func Record(env *Env, st *state.State, e journal.Entry) {
	journal.Append(env.Paths.Journal, e)
	if st == nil || st.Box.Notify.URL == "" {
		return
	}
	interesting := e.Result == "red" || e.Verb == "propose" || e.Verb == "approve" || e.Verb == "reject" || e.Verb == "ready"
	if !interesting {
		return
	}
	go PostEvent(env, st.Box.Notify.URL, e)
}

// PostEvent posts one entry to the webhook, best effort with one retry; a
// failure is its own journal line.
func PostEvent(env *Env, url string, e journal.Entry) {
	body, _ := json.Marshal(e)
	client := &http.Client{Timeout: 5 * time.Second}
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		resp, err := client.Post(url, "application/json", bytes.NewReader(body))
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 300 {
				return
			}
			last = &httpErr{resp.Status}
		} else {
			last = err
		}
		time.Sleep(2 * time.Second)
	}
	journal.Append(env.Paths.Journal, journal.Entry{Actor: "laib", Verb: "notify", Result: "red", Detail: "webhook " + url + ": " + last.Error()})
}

type httpErr struct{ status string }

func (h *httpErr) Error() string { return h.status }

// Tail is the last n bytes of s, marked with … when cut: a journal line
// keeps the end of a long output, where the error is.
func Tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// TokenPath is the hand-token store, next to the state repo.
func TokenPath(env *Env) string { return filepath.Join(env.Paths.Home, "tokens.json") }

// Refused prints why and returns ExitRefused.
func Refused(env *Env, msg string) int {
	fmt.Fprintln(env.Errw(), "laib:", msg)
	return ExitRefused
}

// InboxDir holds the proposals' inbox entries.
func InboxDir(env *Env) string { return env.Paths.Home + "/inbox" }

// Verb is one CLI command. tier is the policy tier it needs (docs/STATE.md).
type Verb struct {
	Name string
	Tier string
	Help string
	Run  func(ctx context.Context, env *Env, args []string) int
	// Flags are the flags it takes; non-nil, any other is refused (#590)
	Flags []string
}

// Guard is every verb's front door (#590): --help or -h shows the usage and
// runs nothing; a verb with Flags refuses one it does not take before it
// runs. What follows "--" is the command's own (exec, look).
func Guard(v Verb) Verb {
	run := v.Run
	v.Run = func(ctx context.Context, env *Env, args []string) int {
		for _, a := range args {
			switch {
			case a == "--":
				return run(ctx, env, args)
			case a == "--help" || a == "-h":
				fmt.Fprintf(env.W(), "laib %s [%s]  %s\n", v.Name, v.Tier, v.Help)
				return ExitOK
			case v.Flags != nil && len(a) > 1 && a[0] == '-' && !slices.Contains(v.Flags, a) && strings.Trim(a, "-0123456789.") != "":
				fmt.Fprintf(env.Errw(), "laib %s: unknown flag %s, nothing ran (laib %s --help)\n", v.Name, a, v.Name)
				return ExitUsage
			}
		}
		return run(ctx, env, args)
	}
	return v
}

// Version is the binary's (initialize, /ready); cmd/laib sets it.
var Version = "dev"
