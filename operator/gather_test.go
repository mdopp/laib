package operator

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// #287: reds close together are one cascade — lldap, then Authelia, then
// Caddy — and become one incident session, not three restarts.
func TestRedsCloseTogetherBecomeOneSession(t *testing.T) {
	var mu sync.Mutex
	var got []Trigger
	g := &Gatherer{Wait: 50 * time.Millisecond, Fire: func(tr Trigger) { mu.Lock(); got = append(got, tr); mu.Unlock() }}
	g.Add("lldap", "restart: unit failed")
	g.Add("auth", "verify: health red")
	g.Add("caddy", "verify: 502 on auth.example")
	time.Sleep(200 * time.Millisecond)
	g.Add("whoami", "verify: health red")
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("%d sessions: %+v", len(got), got)
	}
	if got[0].Service != "lldap" || !strings.Contains(got[0].Detail, "auth: verify: health red") || !strings.Contains(got[0].Detail, "caddy: verify: 502") {
		t.Fatalf("the cascade: %+v", got[0])
	}
	if got[1].Service != "whoami" {
		t.Fatalf("a later red is its own session: %+v", got[1])
	}
}
