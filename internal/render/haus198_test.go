package render

import (
	"regexp"
	"strings"
	"testing"
)

// #198: haus-qmd runs the Vermittler (qmd's SDK, typed searches, rerank off,
// each hit's cosine as `vec`) instead of `qmd mcp --http`, and Haus gets the
// house model to tidy hits — present only when the box has a model.
func TestHausGetsTheVermittlerAndTheHouseModel(t *testing.T) {
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
	for _, name := range []string{"HAUS_LLM_URL", "HAUS_LLM_KEY"} {
		if v, _ := env(out, "quadlet/haus/haus.container", name); v != "" {
			t.Errorf("without the model role %s must be empty (the judge is off), got %q", name, v)
		}
	}

	// with it: the proxy as the haus pod reaches it — the host's port over
	// host.containers.internal, never the pod's own 127.0.0.1 (ADR 0007)
	st.Box.Roles.Model = "llama"
	out, err = Render(st, map[string]string{"MODEL_API_KEY": "k-model"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"HAUS_LLM_URL":   "http://host.containers.internal:11435/v1",
		"HAUS_LLM_MODEL": "gemma-4-e4b",
		"HAUS_LLM_KEY":   "k-model",
	}
	for name, w := range want {
		if v, ok := env(out, "quadlet/haus/haus.container", name); !ok || v != w {
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

	// haus-qmd runs the Vermittler, not qmd's own server
	qmd := out["quadlet/haus/haus-qmd.container"]
	if !strings.Contains(qmd, "\nEntrypoint=qmd-vermittler\n") {
		t.Errorf("haus-qmd.container does not run the Vermittler:\n%s", qmd)
	}
	if regexp.MustCompile(`(?m)^Exec=`).MatchString(qmd) || strings.Contains(qmd, "--http") {
		t.Errorf("haus-qmd.container still passes arguments for qmd's own server:\n%s", qmd)
	}
	if !strings.Contains(qmd, "HealthCmd=qmd-health") {
		t.Errorf("the health probe is gone:\n%s", qmd)
	}
}
