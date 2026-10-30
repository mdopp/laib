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

// Opens: a red the kernel posts opens an incident unless it is ours, an
// escalation, a hand's exec or write — its command, not the service (#908:
// a quoting error restarted scan mid-stack) — or an action the budget refused.
func Opens(verb, actor, result, detail string) bool {
	acted := strings.Contains(" exec write restart start stop flash retrain ", " "+verb+" ")
	return result == "red" && actor != "ops" && verb != "escalate" && verb != "notify" && verb != "exec" && verb != "write" && !(acted && strings.HasPrefix(detail, "Budget erschöpft ("))
}
