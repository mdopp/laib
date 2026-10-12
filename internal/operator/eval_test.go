package operator

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// #43 (8): the eval harness runs the scripted incidents; with the
// scripted model every incident passes and no forbidden tool is ever
// called. Against the household model (OPERATOR_EVAL_MODEL_URL) the same
// harness records pass/fail per incident into werkstatt/evals/results/.
func TestEvalScriptedIncidents(t *testing.T) {
	incidents, err := LoadIncidents(filepath.Join("..", "..", "werkstatt", "evals", "incidents.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(incidents) < 6 {
		t.Fatalf("%d incidents", len(incidents))
	}
	real := os.Getenv("OPERATOR_EVAL_MODEL_URL")
	model := "scripted"
	if real != "" {
		model = "real:" + real
	}
	rep := RunEval(context.Background(), model, incidents, func(ctx context.Context, inc Incident) (Session, error) {
		mcp, _ := fakeMCP(t, inc.Replies)
		llm := &LLM{URL: real, Model: os.Getenv("OPERATOR_EVAL_MODEL")}
		if real == "" {
			srv := fakeLLM(t, inc.Script)
			llm = &LLM{URL: srv.URL, Model: "test"}
		}
		op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: llm, Home: t.TempDir()}
		return op.Run(ctx, inc.Trigger)
	})
	if rep.Wrong != 0 {
		t.Fatalf("wrong mutating actions: %d — %+v", rep.Wrong, rep.Results)
	}
	if real == "" && rep.Passed != rep.Total {
		t.Fatalf("scripted: %d of %d passed: %+v", rep.Passed, rep.Total, rep.Results)
	}
	// the real model's result is the record (werkstatt/evals/results/); the
	// scripted baseline is committed once and not rewritten on every run
	out := filepath.Join(t.TempDir(), "scripted.json")
	if real != "" {
		out = filepath.Join("..", "..", "werkstatt", "evals", "results", "latest-real.json")
	}
	if err := rep.Save(out); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: %d/%d passed, %d wrong mutations → %s", model, rep.Passed, rep.Total, rep.Wrong, out)
}
