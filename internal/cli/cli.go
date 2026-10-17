// Package cli is what every verb of the laib binary shares: the
// environment a verb runs in, the exit codes, loading the state and
// recording a journal entry (#309). Verbs live in their own packages;
// cmd/laib wires them into the verb table.
package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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
