package render

import (
	"regexp"
	"strings"
	"testing"
)

// #198: homes-qmd runs the Vermittler (qmd's SDK, typed searches, rerank off,
// each hit's cosine as `vec`) instead of `qmd mcp --http`, and Homes gets the
// house model to tidy hits — present only when the box has a model.
func TestHomesGetsTheVermittlerAndTheHouseModel(t *testing.T) {
	st := shippedBox(t)
	env := func(out Output, file, name string) (string, bool) {
		t.Helper()
		m := regexp.MustCompile(`(?m)^Environment=` + name + `=(.*)$`).FindStringSubmatch(out[file])
		if m == nil {
			return "", false
		}
		return m[1], true
	}

	// without the model role: the judge is off — no URL, no key
	out, err := Render(st, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"HOMES_LLM_URL", "HOMES_LLM_KEY"} {
		if v, _ := env(out, "quadlet/homes/homes.container", name); v != "" {
			t.Errorf("without the model role %s must be empty (the judge is off), got %q", name, v)
		}
	}

	// with it: the proxy as the homes pod reaches it — the host's port over
	// host.containers.internal, never the pod's own 127.0.0.1 (ADR 0007)
	st.Box.Roles.Model = "llama"
	sec := map[string]string{"MODEL_API_KEY": "k-model"}
	out, err = Render(st, sec)
	out = asContainerSees(st, sec, out)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"HOMES_LLM_URL":   "http://host.containers.internal:11435/v1",
		"HOMES_LLM_MODEL": "qwen3.6-35b-a3b",
		"HOMES_LLM_KEY":   "k-model",
	}
	for name, w := range want {
		if v, ok := env(out, "quadlet/homes/homes.container", name); !ok || v != w {
			t.Errorf("with the model role %s = %q (set: %v), want %q", name, v, ok, w)
		}
	}
	// the same builtin frage and the Werkstatt read: a container's 127.0.0.1 is its own
	if v, _ := env(out, "quadlet/frage/frage.container", "MODEL_URL"); v != "http://host.containers.internal:11435/v1" {
		t.Errorf("frage's MODEL_URL does not reach the model role's proxy from its pod: %q", v)
	}
	if v, _ := env(out, "quadlet/werkstatt/werkstatt-autoloop.container", "MODEL_LEASE"); v != "http://host.containers.internal:11435/api/model-lease" {
		t.Errorf("the Werkstatt's MODEL_LEASE does not reach the lease from its pod: %q", v)
	}

	// homes-qmd runs the Vermittler, not qmd's own server
	qmd := out["quadlet/homes/homes-qmd.container"]
	if !strings.Contains(qmd, "\nEntrypoint=qmd-vermittler\n") {
		t.Errorf("homes-qmd.container does not run the Vermittler:\n%s", qmd)
	}
	if regexp.MustCompile(`(?m)^Exec=`).MatchString(qmd) || strings.Contains(qmd, "--http") {
		t.Errorf("homes-qmd.container still passes arguments for qmd's own server:\n%s", qmd)
	}
	if !strings.Contains(qmd, "HealthCmd=qmd-health") {
		t.Errorf("the health probe is gone:\n%s", qmd)
	}
}
