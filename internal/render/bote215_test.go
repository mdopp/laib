package render

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// #215: Haus's Sprechen field reaches the Bote. Haus gets the Bote's
// OpenAI base as its pod reaches it (host.containers.internal, ADR 0007,
// never an IP) and Hermes' API key, a secret the Bote generates: set while
// the Bote is installed, empty without it (no field). The bote pod
// publishes Hermes' API port beyond loopback, as haus does 8650 for the
// Bote (ADR 0012 §4); lan: false keeps it off the physical interfaces.
func TestHausReachesTheBote(t *testing.T) {
	st := shippedBox(t)
	env := func(out Output, name string) (string, bool) {
		t.Helper()
		m := regexp.MustCompile(`(?m)^Environment=` + name + `=(.*)$`).FindStringSubmatch(out["quadlet/haus/haus.container"])
		if m == nil {
			return "", false
		}
		return m[1], true
	}
	sec := map[string]string{"BOTE_API_KEY": "k-bote-api"}
	out, err := Render(st, sec)
	out = asContainerSees(st, sec, out)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := env(out, "HAUS_BOTE_URL"); !ok || v != "http://host.containers.internal:8642/v1" {
		t.Errorf("HAUS_BOTE_URL = %q (set: %v), want the Bote's API as the haus pod reaches it", v, ok)
	}
	if v, ok := env(out, "HAUS_BOTE_KEY"); !ok || v != "k-bote-api" {
		t.Errorf("HAUS_BOTE_KEY = %q (set: %v), want the Bote's API key", v, ok)
	}
	pod := out["quadlet/bote/bote.pod"]
	if !strings.Contains(pod, "\nPublishPort=8642:8642\n") || strings.Contains(pod, "127.0.0.1:8642") {
		t.Errorf("the bote pod must publish 8642 beyond loopback for the haus pod:\n%s", pod)
	}
	if nft := out["nftables/laib.nft"]; !regexp.MustCompile(`dport \{[^}]*\b8642\b`).MatchString(nft) {
		t.Errorf("8642 is not kept off the LAN:\n%s", nft)
	}

	// without the Bote's key (no Bote installed): the line stays, empty
	out, err = Render(st, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := env(out, "HAUS_BOTE_KEY"); !ok || v != "" {
		t.Errorf("without the Bote HAUS_BOTE_KEY must render empty, got %q (set: %v)", v, ok)
	}
}

// #215: the Bote's pre-start turns Hermes' API server on with the key,
// listening on the pod's interfaces so the published port reaches it.
// The key is the gateway's alone: bote-notify's copy never carries it.
func TestBoteTurnsTheAPIServerOn(t *testing.T) {
	data := t.TempDir()
	cmd := exec.Command("bash", filepath.Join("..", "..", "templates", "bote", "pre-start"))
	cmd.Env = append(os.Environ(), "LAIB_DATA_DIR="+data, "SIGNAL_ACCOUNT=+4915112345678",
		"LAIB_SECRET_BOTE_LAIB_TOKEN=laib-tok", "LAIB_SECRET_BOTE_API_KEY=k-bote-api-0123456789")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pre-start: %v\n%s", err, b)
	}
	hermes, _ := os.ReadFile(filepath.Join(data, "bote", "hermes", ".env"))
	for _, want := range []string{"\nAPI_SERVER_KEY=k-bote-api-0123456789\n", "\nAPI_SERVER_HOST=0.0.0.0\n"} {
		if !strings.Contains(string(hermes), want) {
			t.Errorf("Hermes' .env lacks %q:\n%s", strings.TrimSpace(want), hermes)
		}
	}
	notify, _ := os.ReadFile(filepath.Join(data, "bote", "notify", ".env"))
	if strings.Contains(string(notify), "API_SERVER") || strings.Contains(string(notify), "k-bote-api") {
		t.Errorf("bote-notify's .env carries the API key:\n%s", notify)
	}
}
