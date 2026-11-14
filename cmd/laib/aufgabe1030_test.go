package main

import (
	"context"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/mcp"
)

// #1030: Homes' /mehr waited 4 s for the list of Box-Aufgaben, since the
// whole verb took the exclusive path. Its reading actions run beside other
// reads; adding one still runs alone.
func TestAufgabeListRunsBesideReadsAndAddStillRunsAlone(t *testing.T) {
	env, _ := testBox(t)
	st := mustState(t, env)
	mcp.Mu.RLock() // another read is running
	for _, action := range []string{"list", "zeigen"} {
		args := map[string]any{"action": action}
		if action == "zeigen" {
			args["id"] = "1"
		}
		done := make(chan struct{})
		go func() { mcp.Call(context.Background(), env, st, "ops", "laib_aufgabe", args); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			mcp.Mu.RUnlock()
			t.Fatalf("aufgabe %s waited for a running read", action)
		}
	}
	done := make(chan int, 1)
	go func() {
		_, code := mcp.Call(context.Background(), env, st, "ops", "laib_aufgabe", map[string]any{"action": "add", "titel": "Den Router neu starten"})
		done <- code
	}()
	select {
	case code := <-done:
		mcp.Mu.RUnlock()
		t.Fatalf("aufgabe add ran while a read held the lock (exit %d)", code)
	case <-time.After(300 * time.Millisecond):
	}
	mcp.Mu.RUnlock()
	if code := <-done; code != cli.ExitOK {
		t.Fatalf("aufgabe add: exit %d", code)
	}
}
