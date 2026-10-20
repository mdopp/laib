package templates

import (
	"io/fs"
	"strings"
	"testing"
)

func readT(t *testing.T, name string) string {
	t.Helper()
	b, err := fs.ReadFile(FS, name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// #490: the Bote calls Frage from its own pod at host.containers.internal:8710,
// the box's address, not its loopback: the port is published on every
// address and lan: false keeps the LAN out (as Piper, #242).
func TestFrageAnswersPodsOnTheBoxButNeverTheLAN(t *testing.T) {
	pod := readT(t, "frage/frage.pod")
	if !strings.Contains(pod, "\nPublishPort=8710:8710\n") {
		t.Errorf("frage.pod publishes 8710 on loopback only: the Bote cannot reach it\n%s", pod)
	}
	if !strings.Contains(readT(t, "frage/laib.yml"), "{port: 8710, lan: false}") {
		t.Errorf("8710 is not declared lan: false: publishing it beyond loopback would reach the LAN")
	}
}

// #490: SearXNG's image moved to granian on 8080 while Frage asked 8888.
// The port is set on SearXNG, not taken from the image, and is the one
// Frage asks, inside the pod.
func TestSearxListensWhereFrageAsks(t *testing.T) {
	searx, frage := readT(t, "frage/frage-searx.container"), readT(t, "frage/frage.container")
	if !strings.Contains(frage, "Environment=SEARX_URL=http://127.0.0.1:8888\n") {
		t.Fatalf("Frage does not ask SearXNG pod-locally on 8888:\n%s", frage)
	}
	for _, want := range []string{"Environment=SEARXNG_PORT=8888\n", "Environment=GRANIAN_PORT=8888\n"} {
		if !strings.Contains(searx, want) {
			t.Errorf("frage-searx.container lacks %q: the image's own default port (8080) wins", strings.TrimSpace(want))
		}
	}
}

// #490: settings.yml is written before SearXNG starts (pre-start, on every
// apply), never after it by a post-deploy that skips an existing file: the
// image writes its own on the first start, without the JSON format.
func TestSearxSettingsBeforeTheStart(t *testing.T) {
	if _, err := fs.Stat(FS, "frage/post-deploy"); err == nil {
		t.Error("frage still has a post-deploy: settings.yml after the start races the image's own")
	}
	pre := readT(t, "frage/pre-start")
	if strings.Contains(pre, "! -f") || !strings.Contains(pre, "json") {
		t.Errorf("pre-start does not write settings.yml unconditionally with the JSON format:\n%s", pre)
	}
}
