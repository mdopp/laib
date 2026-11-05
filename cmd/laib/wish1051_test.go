package main

import (
	"context"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
)

// #1051: Homes' wish for a module and its stand, over MCP as Homes calls
// them, through the real laib-haushalt: it calls back laib_aufgabe and
// laib_inbox, which take the MCP lock. Held across the door, the call back
// waited until the caller gave up (Homes: 3 s and 30 s), and every other
// MCP call with it.
func TestAWishBehindTheDoorHoldsNoLockForItsCallBack(t *testing.T) {
	env, _ := testBox(t) // the policy every box gets: homes may wish
	t.Setenv("GITHUB_API_URL", newFakeGitHub(t).srv.URL)
	withHaushalt(t, env)
	st := mustState(t, env)
	for _, args := range []map[string]any{
		{"text": "Standortverlauf", "modul": "standort", "von": "lena"},
		{"stand": "standort=reitti"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		start := time.Now()
		out, code := mcp.Call(ctx, env, st, "homes", "laib_wish", args)
		cancel()
		if took := time.Since(start); code != cli.ExitOK || took > 8*time.Second {
			t.Fatalf("%v: exit %d after %s, the call back waited for the lock: %s", args, code, took.Round(time.Millisecond), out)
		}
	}
}

// #1051, the control: the kernel's own form of wish, the Operator's wish
// as a proposal, still runs alone over MCP.
func TestTheOperatorsWishStillTakesTheLock(t *testing.T) {
	env, _ := testBox(t)
	st := mustState(t, env)
	mcp.Mu.Lock()
	done := make(chan int, 1)
	go func() {
		_, code := mcp.Call(context.Background(), env, st, "ops", "laib_wish", map[string]any{"text": "Ein Dienst für Rezepte.", "saw": "drei Fragen danach", "better": "familie"})
		done <- code
	}()
	select {
	case code := <-done:
		mcp.Mu.Unlock()
		t.Fatalf("the Operator's wish ran while another call held the lock (exit %d)", code)
	case <-time.After(300 * time.Millisecond):
	}
	mcp.Mu.Unlock()
	if code := <-done; code != cli.ExitOK {
		t.Fatalf("the Operator's wish: exit %d", code)
	}
	if open, _ := proposal.List(cli.InboxDir(env), true); len(open) != 1 {
		t.Fatalf("the Operator's wish waits as one proposal: %+v", open)
	}
}
