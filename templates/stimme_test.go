package templates

import (
	"io/fs"
	"strings"
	"testing"
)

// #242: a pod reaches Piper at host.containers.internal:10200 (foundry-chronicle's
// chronik speaks its consent announcement through it), and host.containers.internal
// is the box's address, not its loopback. lan: false keeps the LAN out.
func TestPiperAnswersPodsOnTheBoxButNeverTheLAN(t *testing.T) {
	unit, err := fs.ReadFile(FS, "stimme/stimme-piper.container")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unit), "--uri tcp://0.0.0.0:10200") {
		t.Errorf("Piper binds loopback only: a pod cannot reach it at host.containers.internal:10200")
	}
	contract, err := fs.ReadFile(FS, "stimme/laib.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contract), "{port: 10200, lan: false}") {
		t.Errorf("Piper's port is not declared lan: false: binding beyond loopback would reach the LAN")
	}
}
