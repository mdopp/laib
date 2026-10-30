package diag

import (
	"strings"
	"testing"
	"time"
)

// #931: after llama restarted, every look was skipped for 45 min because the
// household model was not loaded (a look never wakes it, #550/#792), and diag
// called that gelb „letzter Blick ist 45m alt … Tick hängt“. A look that waits
// with a reason (/ready's work) is the Operator answering, not hanging: it
// says why it waits, as info. Without a reason the old age finding stands.
func TestAnOperatorThatWaitsWithAReasonIsNotGelb(t *testing.T) {
	now, tick := time.Now(), 10*time.Minute
	f := OpFacts{Enabled: true, UnitActive: true, Answers: true, Looked: now.Add(-45 * time.Minute), Now: now, Tick: tick,
		Work: OpWork{Waiting: "das Modell qwen3.6-35b-a3b ist nicht geladen (unloaded); die Sitzung wäre sein Kaltstart"}}
	state, finding := OperatorState(f)
	if state != "wartet" || finding == nil || finding.Severity != "info" || !strings.Contains(finding.Detail, "Kaltstart") {
		t.Fatalf("a reasoned wait: %q %+v, want wartet and an info finding with the reason", state, finding)
	}
	f.Work.Waiting = ""
	if state, finding := OperatorState(f); state != "stumm" || finding == nil || finding.Severity != "gelb" {
		t.Fatalf("no reason: %q %+v, want stumm/gelb", state, finding)
	}
}
