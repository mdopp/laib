package templates

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #1166: openWakeWord left the host network for its own pod, 10400 on the
// box's loopback, where Home Assistant (host network) reaches it as before,
// with the three hardening lines. The golden case nuc renders these very
// files; a template change that the case does not see would make the golden
// file a promise about something else.
func TestOpenWakeWordGoldenIsTheTemplates(t *testing.T) {
	for _, f := range []string{"stimme-openwakeword.container", "stimme-openwakeword.pod"} {
		tpl, err := fs.ReadFile(FS, "stimme/"+f)
		if err != nil {
			t.Fatal(err)
		}
		golden, err := os.ReadFile(filepath.Join("..", "testdata", "golden", "nuc", "state", "services", "stimme", f))
		if err != nil {
			t.Fatal(err)
		}
		if string(tpl) != string(golden) {
			t.Errorf("testdata/golden/nuc/state/services/stimme/%s differs from templates/stimme/%s: copy it, then UPDATE_GOLDEN=1 go test ./internal/render/", f, f)
		}
	}
	unit, _ := fs.ReadFile(FS, "stimme/stimme-openwakeword.container")
	pod, _ := fs.ReadFile(FS, "stimme/stimme-openwakeword.pod")
	for _, want := range []string{"Pod=stimme-openwakeword.pod", "ReadOnly=true", "NoNewPrivileges=true", "DropCapability=all"} {
		if !strings.Contains(string(unit), "\n"+want+"\n") {
			t.Errorf("stimme-openwakeword.container lacks %s", want)
		}
	}
	if strings.Contains(string(unit), "Network=host") {
		t.Error("openWakeWord is on the host network again: every loopback listener of the box is in its reach")
	}
	if !strings.Contains(string(pod), "\nPublishPort=127.0.0.1:10400:10400\n") {
		t.Error("the pod does not publish 10400 on the box's loopback: Home Assistant's Wyoming entry is 127.0.0.1:10400")
	}
}
