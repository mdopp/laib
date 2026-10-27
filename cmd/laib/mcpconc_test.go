package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/state"
)

// withVerb registers a test verb an agent may reach, for one test.
func withVerb(t *testing.T, v cli.Verb) {
	t.Helper()
	saved := mcp.Verbs
	v.MCP = true
	mcp.Verbs = append(append([]cli.Verb(nil), mcp.Verbs...), v)
	t.Cleanup(func() { mcp.Verbs = saved })
}

// #289: read verbs over MCP run side by side — two calls are inside the
// verb at the same time, nothing serializes them.
func TestMcpReadCallsRunConcurrently(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(2)
	both := make(chan struct{})
	go func() { wg.Wait(); close(both) }()
	withVerb(t, cli.Verb{Name: "probe289", Tier: "read", Help: "test", Run: func(_ context.Context, env *cliEnv, _ []string) int {
		wg.Done()
		select {
		case <-both:
			return cli.ExitOK
		case <-time.After(3 * time.Second):
			return env.Fail(errors.New("allein: der andere Aufruf wartet"))
		}
	}})
	st := &state.State{Policy: state.Policy{Hands: map[string]state.Hand{"ops": {Ceiling: "apply"}}}}
	outs := make([]string, 2)
	codes := make([]int, 2)
	var done sync.WaitGroup
	for i := range 2 {
		done.Add(1)
		go func() {
			defer done.Done()
			outs[i], codes[i] = mcp.Call(context.Background(), &cliEnv{}, st, "ops", "laib_probe289", map[string]any{"args": ""})
		}()
	}
	done.Wait()
	if codes[0] != cli.ExitOK || codes[1] != cli.ExitOK {
		t.Fatalf("read calls were serialized: %q %v", outs, codes)
	}
}

// #289: a verb's error reaches the MCP answer through its own writer; the
// process's stderr is never swapped, so nothing else lands in a reply.
func TestMcpErrorsGoThroughTheCallsWriterNotStderr(t *testing.T) {
	orig := os.Stderr
	withVerb(t, cli.Verb{Name: "probe289e", Tier: "read", Help: "test", Run: func(_ context.Context, env *cliEnv, _ []string) int {
		if os.Stderr != orig {
			return env.Fail(errors.New("stderr wurde umgebogen"))
		}
		return env.Fail(errors.New("kaputt: Datei fehlt"))
	}})
	st := &state.State{Policy: state.Policy{Hands: map[string]state.Hand{"ops": {Ceiling: "apply"}}}}
	out, code := mcp.Call(context.Background(), &cliEnv{}, st, "ops", "laib_probe289e", map[string]any{"args": ""})
	if code == cli.ExitOK || !strings.Contains(out, "kaputt: Datei fehlt") {
		t.Fatalf("%d %q", code, out)
	}
}
