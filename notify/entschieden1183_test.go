package notify

import (
	"context"
	"strings"
	"testing"
	"time"
)

// #1183 (owner, 2026-11-11), was #1113: once a proposal has left the Inbox
// and, when approved, its run has ended, bote-notify is told how it ended
// (POST /entschieden, once) and marks the chat line that announced it. An
// approved one still waiting for its run, or in it, is not told yet; one
// still open, or one the chat never took, is not asked about.
func TestADecidedProposalsChatLineIsMarkedOnceItsRunEnded(t *testing.T) {
	f := newFakeLaib(t)
	b := newFakeBote(t)
	c := chatConfig(t, f, b, "127.0.0.1:1")
	f.inbox = `[{"id":17,"status":"open","reason":"a","card":{"summary":"eins"}},{"id":18,"status":"open","reason":"b","card":{"summary":"zwei"}},{"id":19,"status":"open","reason":"c","card":{"summary":"drei"}}]`
	now := time.Now()
	if err := Sweep(context.Background(), c, now); err != nil {
		t.Fatal(err)
	}
	if len(b.ended()) != 0 {
		t.Fatalf("nothing decided, nothing told: %q", b.ended())
	}
	// #17 approved, its run not yet through; #18 rejected; #19 stale, filed again as #20
	f.inbox = `[{"id":20,"status":"open","reason":"c","card":{"summary":"drei"}},{"id":17,"status":"approved","outcome":"waiting"},{"id":18,"status":"rejected","outcome":"later"},{"id":19,"status":"stale","refiled":20}]`
	Sweep(context.Background(), c, now.Add(time.Minute))
	got := strings.Join(b.ended(), "")
	if !strings.Contains(got, `{"freigabe":18,"status":"rejected","outcome":"later","neu":0}`) || !strings.Contains(got, `{"freigabe":19,"status":"stale","outcome":"","neu":20}`) {
		t.Fatalf("the rejected and the stale one are told how they ended: %q", got)
	}
	if strings.Contains(got, `"freigabe":17`) {
		t.Fatalf("an approved one is told only once its run has ended: %q", got)
	}
	f.inbox = `[{"id":20,"status":"open","reason":"c","card":{"summary":"drei"}},{"id":17,"status":"approved","outcome":"waiting","step":"spielt whoami ein"},{"id":18,"status":"rejected","outcome":"later"}]`
	Sweep(context.Background(), c, now.Add(2*time.Minute))
	if n := len(b.ended()); n != 2 {
		t.Fatalf("in its run it still waits, the others are told once: %q", b.ended())
	}
	f.inbox = `[{"id":20,"status":"open","reason":"c","card":{"summary":"drei"}},{"id":17,"status":"approved","outcome":"effective"}]`
	Sweep(context.Background(), c, now.Add(3*time.Minute))
	Sweep(context.Background(), c, now.Add(4*time.Minute))
	if d := b.ended(); len(d) != 3 || strings.TrimSpace(d[2]) != `{"freigabe":17,"status":"approved","outcome":"effective","neu":0}` {
		t.Fatalf("the approved one, once applied, is told once: %q", d)
	}
	for _, m := range b.messages() {
		if _, has := m["freigabe"]; has != strings.HasPrefix(m["text"], "Freigabe #") {
			t.Errorf("only a proposal's notice carries its number: %q", m)
		}
	}
}

// #1183: a red run is told as rolled-back; one no longer in the newest
// decided ones is told with its number alone (bote-notify: no mark).
func TestARolledBackOrUnknownEndIsToldAsSuch(t *testing.T) {
	f := newFakeLaib(t)
	b := newFakeBote(t)
	c := chatConfig(t, f, b, "127.0.0.1:1")
	f.inbox = `[{"id":17,"status":"open","reason":"a","card":{"summary":"eins"}},{"id":18,"status":"open","reason":"b","card":{"summary":"zwei"}}]`
	now := time.Now()
	Sweep(context.Background(), c, now)
	f.inbox = `[{"id":17,"status":"approved","outcome":"rolled-back"}]`
	Sweep(context.Background(), c, now.Add(time.Minute))
	got := strings.Join(b.ended(), "")
	if !strings.Contains(got, `{"freigabe":17,"status":"approved","outcome":"rolled-back","neu":0}`) || !strings.Contains(got, `{"freigabe":18}`) {
		t.Fatalf("the red run and the unknown one: %q", got)
	}
}

// #1113: a decision never waits on the Bote: bote-notify away, the sweep
// ends as it would and the proposal is forgotten all the same.
func TestTheMarkIsBestEffort(t *testing.T) {
	f := newFakeLaib(t)
	b := newFakeBote(t)
	c := chatConfig(t, f, b, "127.0.0.1:1")
	f.inbox = `[{"id":17,"status":"open","reason":"a","card":{"summary":"eins"}}]`
	now := time.Now()
	Sweep(context.Background(), c, now)
	b.srv.Close()
	f.inbox = `[{"id":17,"status":"rejected"}]`
	if err := Sweep(context.Background(), c, now.Add(time.Minute)); err != nil {
		t.Fatalf("a failed mark is no error of the sweep: %v", err)
	}
	b2 := newFakeBote(t)
	c.BoteURL = b2.srv.URL
	Sweep(context.Background(), c, now.Add(2*time.Minute))
	if len(b2.ended()) != 0 {
		t.Fatalf("a mark is tried once, not again: %q", b2.ended())
	}
}
