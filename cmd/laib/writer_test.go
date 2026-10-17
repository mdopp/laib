package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/envelope"
	"github.com/mdopp/laib/internal/sysd"
)

// #43 (7): verbs write to the writer the cliEnv carries; MCP passes a
// buffer. The process-wide os.Stdout is never swapped, so output of
// another goroutine (the converge loop, an HTTP handler) cannot land in
// an MCP reply, and a verb's output cannot fill a pipe nobody reads.
func TestMCPCallDoesNotTouchTheProcessStdout(t *testing.T) {
	c := newCbox(t)
	st, _ := cli.LoadState(c.env)
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	stray := make(chan struct{})
	go func() { // another goroutine writing to the process stdout meanwhile
		<-stray
		os.Stdout.WriteString("STRAY-LINE\n")
	}()
	out, code := mcpCall(context.Background(), c.env, st, "human", "laib_status", map[string]any{"args": ""})
	close(stray)
	time.Sleep(50 * time.Millisecond)
	w.Close()
	os.Stdout = old
	var captured bytes.Buffer
	io.Copy(&captured, r)
	if code != cli.ExitOK || !strings.Contains(out, "\"ready\"") {
		t.Fatalf("status over MCP: %d %q", code, out)
	}
	if strings.Contains(out, "STRAY") {
		t.Fatal("another goroutine's stdout landed in the MCP reply: the process stdout was swapped")
	}
	if !strings.Contains(captured.String(), "STRAY-LINE") {
		t.Fatal("the stray line must still reach the process stdout")
	}
}

// #43 (7): escalate builds the inspect bundle through a buffer — 200 KiB
// of inspect output must not block on a pipe while mcpMu is held.
func TestEscalateCompletesWithALargeInspectBundle(t *testing.T) {
	c := newCbox(t)
	// podman inspect of a container is long; here 200 KiB of it
	inner := sysd.Run
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "podman" && len(args) > 0 && args[0] == "inspect" {
			return strings.Repeat("{\"Id\":\"abc\",\"State\":{\"Status\":\"running\"}}\n", 200*1024/48), nil
		}
		return inner(ctx, name, args...)
	}
	t.Cleanup(func() { sysd.Run = inner })
	done := make(chan int, 1)
	go func() {
		done <- envelope.Escalate(context.Background(), &cliEnv{Paths: c.env.Paths, Actor: "ops", Out: &bytes.Buffer{}}, []string{"caddy", "test: large bundle"})
	}()
	select {
	case rc := <-done:
		if rc != cli.ExitOK {
			t.Fatalf("escalate rc %d", rc)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("escalate hung (a pipe nobody reads)")
	}
	files, _ := filepath.Glob(filepath.Join(c.env.Paths.Home, "escalations", "*.md"))
	if len(files) != 1 {
		t.Fatalf("escalation files: %v", files)
	}
	b, _ := os.ReadFile(files[0])
	if len(b) < 64*1024 {
		t.Fatalf("the bundle is only %d bytes; the test needs more than a pipe buffer to mean anything", len(b))
	}
}

// #43 (3, 6, 5): diag is an MCP tool; operator enable wires the webhook;
// a silent operator is red after three ticks, not 'startet' for ever.
func TestOperatorWiringAndSilence(t *testing.T) {
	if !mcpVerbs["diag"] {
		t.Fatal("the prompt asks for laib_diag; diag must be an MCP tool")
	}
	c := newCbox(t)
	b, _ := os.ReadFile(filepath.Join(c.env.Paths.State, "box.yml"))
	text := regexp.MustCompile(`(?m)^notify:\n(  .*\n)*`).ReplaceAllString(string(b), "") // a box without a webhook of its own
	os.WriteFile(filepath.Join(c.env.Paths.State, "box.yml"), append([]byte(text), []byte("model:\n  url: http://10.0.0.9:11435/v1\n")...), 0o644)
	if rc := cmdOperator(context.Background(), c.env, []string{"enable"}); rc != cli.ExitOK {
		t.Fatalf("enable rc %d", rc)
	}
	st, err := cli.LoadState(c.env)
	if err != nil {
		t.Fatal(err)
	}
	if st.Box.Notify.URL != "http://127.0.0.1:5901/event" {
		t.Fatalf("enable must point notify.url at the operator so reds wake it: %q", st.Box.Notify.URL)
	}
	now := time.Now()
	tick := 10 * time.Minute
	state, f := diag.OperatorState(diag.OpFacts{Enabled: true, ModelURL: "http://m/v1", UnitActive: true, Answers: true, Now: now, Tick: tick, EnabledAt: now.Add(-tick)})
	if state != "startet" || f != nil {
		t.Fatalf("right after enable: %s %+v", state, f)
	}
	state, f = diag.OperatorState(diag.OpFacts{Enabled: true, ModelURL: "http://m/v1", UnitActive: true, Answers: true, Now: now, Tick: tick, EnabledAt: now.Add(-4 * tick)})
	if state != "stumm" || f == nil || f.Severity != "rot" {
		t.Fatalf("four ticks without a note must be red: %s %+v", state, f)
	}
}
