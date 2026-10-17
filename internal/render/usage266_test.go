package render

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// #266: Haus counts what the family uses into usage-metrics. It gets the
// service as its pod reaches it (host.containers.internal, ADR 0007, never an
// IP) and usage-metrics' bearer, a podman secret (#224): on no argv and in no
// unit file. Without usage-metrics installed the token renders empty and Haus
// counts nothing.
func TestHausCountsIntoUsageMetrics(t *testing.T) {
	st := shippedBox(t)
	env := func(out Output, name string) (string, bool) {
		t.Helper()
		m := regexp.MustCompile(`(?m)^Environment=` + name + `=(.*)$`).FindStringSubmatch(out["quadlet/haus/haus.container"])
		if m == nil {
			return "", false
		}
		return m[1], true
	}
	sec := map[string]string{"USAGE_METRICS_TOKEN": "um-tok-266"}
	raw, err := Render(st, sec)
	if err != nil {
		t.Fatal(err)
	}
	for p, c := range raw {
		if strings.HasPrefix(p, "quadlet/") && strings.Contains(c, "um-tok-266") {
			t.Errorf("%s carries usage-metrics' token, where ps and the unit file show it", p)
		}
	}
	out := asContainerSees(st, sec, raw)
	if v, ok := env(out, "HAUS_USAGE_URL"); !ok || v != "http://host.containers.internal:8094" {
		t.Errorf("HAUS_USAGE_URL = %q (set: %v), want usage-metrics as the haus pod reaches it", v, ok)
	}
	if v, ok := env(out, "HAUS_USAGE_TOKEN"); !ok || v != "um-tok-266" {
		t.Errorf("HAUS_USAGE_TOKEN = %q (set: %v), want usage-metrics' token", v, ok)
	}

	out, err = Render(st, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := env(out, "HAUS_USAGE_TOKEN"); !ok || v != "" {
		t.Errorf("without usage-metrics HAUS_USAGE_TOKEN must render empty, got %q (set: %v)", v, ok)
	}
}

// #266: the Bote counts through a Hermes plugin (ADR 0016: stock Hermes,
// its own extension point). With usage-metrics' token pre-start hands Hermes
// the URL and the token, and never bote-notify's copy; post-deploy places the
// plugin and enables it. Without the token there is no plugin and no line.
func TestBoteCountsThroughAHermesPlugin(t *testing.T) {
	data := t.TempDir()
	run := func(t *testing.T, token string) {
		t.Helper()
		srv := filepath.Join("..", "..", "templates", "bote")
		env := append(os.Environ(), "LAIB_DATA_DIR="+data, "LAIB_RENDER_DIR="+data+"/none", "SIGNAL_ACCOUNT=+4915112345678",
			"MODEL_URL=http://127.0.0.1:1/v1", "MODEL_NAME=m", "THINKING_MODEL=t",
			"LAIB_SECRET_BOTE_LAIB_TOKEN=laib-tok", "USAGE_URL=http://host.containers.internal:8094")
		if token != "" {
			env = append(env, "LAIB_SECRET_USAGE_METRICS_TOKEN="+token)
		}
		cmd := exec.Command("bash", filepath.Join(srv, "post-deploy"))
		cmd.Env = env
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("post-deploy: %v\n%s", err, b)
		}
	}
	read := func(p ...string) string { b, _ := os.ReadFile(filepath.Join(p...)); return string(b) }

	run(t, "um-tok-266")
	h := filepath.Join(data, "bote", "hermes")
	for _, want := range []string{"\nUSAGE_METRICS_URL=http://host.containers.internal:8094\n", "\nUSAGE_METRICS_TOKEN=um-tok-266\n"} {
		if !strings.Contains(read(h, ".env"), want) {
			t.Errorf("Hermes' .env lacks %q", strings.TrimSpace(want))
		}
	}
	if strings.Contains(read(data, "bote", "notify", ".env"), "um-tok-266") {
		t.Error("bote-notify's .env carries usage-metrics' token")
	}
	plugin, _ := os.ReadFile(filepath.Join("..", "..", "templates", "bote", "nutzung.py"))
	if got := read(h, "plugins", "laib-nutzung", "__init__.py"); got == "" || got != string(plugin) {
		t.Error("post-deploy did not place templates/bote/nutzung.py as the plugin laib-nutzung")
	}
	if !strings.Contains(read(h, "plugins", "laib-nutzung", "plugin.yaml"), "name: laib-nutzung") {
		t.Error("the plugin has no manifest naming it laib-nutzung")
	}
	if !regexp.MustCompile(`(?m)^plugins:\n  enabled: \[laib-nutzung\]$`).MatchString(read(h, "config.yaml")) {
		t.Errorf("config.yaml does not enable laib-nutzung:\n%s", read(h, "config.yaml"))
	}
	if !strings.Contains(read(h, "SOUL.md"), "[unbeantwortet]") {
		t.Error("the SOUL does not tell the model how to mark a turn it could not help with")
	}

	// the token gone (usage-metrics removed): the next apply takes the plugin out again
	run(t, "")
	if _, err := os.Stat(filepath.Join(h, "plugins", "laib-nutzung")); err == nil {
		t.Error("without usage-metrics' token the plugin must not stay")
	}
	if c := read(h, "config.yaml"); strings.Contains(c, "laib-nutzung") || strings.Contains(read(h, ".env"), "USAGE_METRICS") {
		t.Error("without usage-metrics' token neither config.yaml nor .env may name it")
	}
	if strings.Contains(read(h, "SOUL.md"), "[unbeantwortet]") {
		t.Error("without the plugin the SOUL must not ask for the marker nobody strips")
	}
}
