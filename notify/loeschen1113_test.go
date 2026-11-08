package notify

import (
	"context"
	"strings"
	"testing"
	"time"
)

// #1113 (owner, 2026-11-08): once a proposal has left the Inbox (approved,
// rejected, stale, refiled), the chat line that announced it goes: one POST
// /loeschen with its number, once. A proposal still open, or one the chat
// never took, is not asked about; the other messages carry no number.
func TestADecidedProposalsChatLineIsDeleted(t *testing.T) {
	f := newFakeLaib(t)
	b := newFakeBote(t)
	c := chatConfig(t, f, b, "127.0.0.1:1")
	f.inbox = `[{"id":17,"reason":"a","card":{"summary":"eins"}},{"id":18,"reason":"b","card":{"summary":"zwei"}}]`
	now := time.Now()
	if err := Sweep(context.Background(), c, now); err != nil {
		t.Fatal(err)
	}
	if len(b.deleted()) != 0 {
		t.Fatalf("nothing decided, nothing deleted: %q", b.deleted())
	}
	f.inbox = `[{"id":18,"reason":"b","card":{"summary":"zwei"}}]` // #17 decided
	Sweep(context.Background(), c, now.Add(time.Minute))
	Sweep(context.Background(), c, now.Add(2*time.Minute))
	if len(b.deleted()) != 1 || strings.TrimSpace(b.deleted()[0]) != `{"freigabe":17}` {
		t.Fatalf("the decided one's line goes, once: %q", b.deleted())
	}
	f.red = true
	Sweep(context.Background(), c, now.Add(3*time.Minute))
	Sweep(context.Background(), c, now.Add(14*time.Minute))
	for _, m := range b.messages() {
		if _, has := m["freigabe"]; has != strings.HasPrefix(m["text"], "Freigabe #") {
			t.Errorf("only a proposal's notice carries its number: %q", m)
		}
	}
}

// #1113: a decision never waits on the Bote: bote-notify away, the sweep
// ends as it would and the proposal is forgotten all the same.
func TestTheDeleteIsBestEffort(t *testing.T) {
	f := newFakeLaib(t)
	b := newFakeBote(t)
	c := chatConfig(t, f, b, "127.0.0.1:1")
	f.inbox = `[{"id":17,"reason":"a","card":{"summary":"eins"}}]`
	now := time.Now()
	Sweep(context.Background(), c, now)
	b.srv.Close()
	f.inbox = `[]`
	if err := Sweep(context.Background(), c, now.Add(time.Minute)); err != nil {
		t.Fatalf("a failed delete is no error of the sweep: %v", err)
	}
	b2 := newFakeBote(t)
	c.BoteURL = b2.srv.URL
	Sweep(context.Background(), c, now.Add(2*time.Minute))
	if len(b2.deleted()) != 0 {
		t.Fatalf("a delete is tried once, not again: %q", b2.deleted())
	}
}
