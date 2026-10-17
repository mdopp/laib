package operator

import (
	"strings"
	"sync"
	"time"
)

// Gatherer turns reds close together into one incident session (#287): a
// cascade — lldap, then Authelia, then Caddy — is one cause, and three
// sessions would restart the three symptoms. The first red opens a window
// of Wait; every red inside it joins; then Fire gets one trigger for the
// first service with every red in its detail.
type Gatherer struct {
	Wait time.Duration
	Fire func(Trigger)
	mu   sync.Mutex
	open *Trigger
}

// Add takes one red.
func (g *Gatherer) Add(service, detail string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.open != nil {
		g.open.Detail += "; " + service + ": " + detail
		return
	}
	g.open = &Trigger{Kind: "event", Service: service, Detail: service + ": " + detail}
	time.AfterFunc(g.Wait, func() {
		g.mu.Lock()
		tr := *g.open
		g.open = nil
		g.mu.Unlock()
		if strings.Count(tr.Detail, "; ") > 0 {
			tr.Detail = "mehrere Dienste rot — " + tr.Detail
		}
		g.Fire(tr)
	})
}
