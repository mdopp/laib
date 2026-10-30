package main

import (
	"fmt"
	"strings"
	"testing"
)

// #857: laib-notify's message links to /inbox#N, so the open card carries
// its number as its anchor.
func TestAnOpenCardIsItsOwnAnchor(t *testing.T) {
	env, _ := testBox(t)
	p := proposeService(t, env, "w1")
	body := inboxMux(env)("GET", "/inbox", nil)
	if !strings.Contains(body, fmt.Sprintf("<section class=card id=%d ", p.ID)) {
		t.Fatalf("the card of #%d has no anchor:\n%s", p.ID, body)
	}
}
