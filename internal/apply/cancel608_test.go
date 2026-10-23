package apply

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/state"
)

// #608: a cancelled apply (the web request's connection closed) killed the
// pod restart, then verify polled every unit for its full 60 s and the
// health for its 90 s on the dead context: 8½ minutes of held lock and a
// red "unknown" for units that were running. A done context ends the wait
// at once and says why.
func TestVerifyStopsOnACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	a := waitActive(ctx, "bote", "bote-pod.service")
	h := waitHTTP(ctx, "bote", state.Health{HTTP: "http://127.0.0.1:1/health"})
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("verify waited %s on a cancelled context", d)
	}
	for _, c := range []Check{a, h} {
		if c.OK || !strings.Contains(c.Detail, "context canceled") {
			t.Errorf("%s: OK=%v detail %q, want red with the cancel named", c.What, c.OK, c.Detail)
		}
	}
}
