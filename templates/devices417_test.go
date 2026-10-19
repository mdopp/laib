package templates

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// #417: a container_t process may not open a USB serial device under
// SELinux enforcing, so the Z-Wave stick passed with AddDevice= gave
// EACCES and the whole Z-Wave network was down after the move. Every
// built-in unit that passes a device carries SecurityLabelDisable=true
// (ServiceBay ran zwave privileged; this is the narrower switch). The VM
// has no stick, so this pins the rendered setting instead.
func TestEveryDeviceContainerDisablesTheLabel(t *testing.T) {
	units, _ := filepath.Glob(filepath.Join("*", "*.container"))
	add := regexp.MustCompile(`(?m)^AddDevice=`)
	n := 0
	for _, u := range units {
		b, _ := os.ReadFile(u)
		if !add.Match(b) {
			continue
		}
		n++
		if !regexp.MustCompile(`(?m)^SecurityLabelDisable=true$`).Match(b) {
			t.Errorf("%s passes a device without SecurityLabelDisable=true: SELinux refuses the open (EACCES)", u)
		}
	}
	if n < 5 {
		t.Fatalf("only %d units with AddDevice=: the glob found the wrong files", n)
	}
}
