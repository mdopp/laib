package operator

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// #550 point 5: an Operator session between two of a person's turns
// evicted the Bote's prompt cache. While a person asked the model (Bote,
// Haus, Frage: whatever the lease proxy did not see marked as background)
// less than two minutes ago, no session asks it: skipped with its reason,
// and the next step tries again.
func TestNoSessionRightAfterAPersonAskedTheModel(t *testing.T) {
	asked := func(ago time.Duration) string {
		return fmt.Sprintf(`{"state":"none","mode":"haushalt","holder":"","allowed":["qwen"],"last_human_at":%d}`, time.Now().Add(-ago).Unix())
	}
	lease, catalogue := asked(30*time.Second), loaded
	llm, n := fakeLease(t, &lease, &catalogue, []Message{{Role: "assistant", Content: "fertig"}})
	mcp, _ := fakeMCP(t, map[string]string{"laib_diag": "[" + watchdog + "]"})
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL + "/v1", Model: "qwen"}, Home: t.TempDir()}
	look := skillFor(t, Trigger{Kind: "tick"}, "routineblick")
	s, err := op.Run(context.Background(), look, Trigger{Kind: "tick"})
	if err != nil || s.Outcome != "skipped" || !strings.Contains(s.Why, "Person") || *n != 0 {
		t.Fatalf("a session asked the model 30 s after a person: %+v %v (asked %d)", s, err, *n)
	}
	lease = asked(5 * time.Minute)
	if s, _ := op.Run(context.Background(), look, Trigger{Kind: "tick"}); s.Outcome == "skipped" || *n == 0 {
		t.Fatalf("a person's request five minutes ago still holds the session back: %+v", s)
	}
}

// The Operator marks its own model requests, so the proxy does not count
// them as a person's.
func TestTheOperatorMarksItsModelRequestsAsBackground(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Laib-Background")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer srv.Close()
	if _, err := (&LLM{URL: srv.URL + "/v1", Model: "qwen"}).Chat(context.Background(), []Message{{Role: "user", Content: "hallo"}}, nil); err != nil {
		t.Fatal(err)
	}
	if got != "operator" {
		t.Fatalf("X-Laib-Background is %q, not operator", got)
	}
}
