package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #252: a rootless container under pasta has the host's own address, so
// auth.<domain> pinned to ${LAN_IP} stays inside the container (ServiceBay
// #817); the host gateway is the host. Podman refuses extra host entries on a
// container that joins a pod: they belong on the .pod.
func TestAddHostIsTheHostGatewayAndOnThePod(t *testing.T) {
	units, _ := filepath.Glob("../../templates/*/*.container")
	pods, _ := filepath.Glob("../../templates/*/*.pod")
	for _, u := range append(units, pods...) {
		b, _ := os.ReadFile(u)
		for _, line := range strings.Split(string(b), "\n") {
			if !strings.HasPrefix(line, "AddHost=") {
				continue
			}
			if !strings.HasSuffix(line, ":host-gateway") {
				t.Errorf("%s: %s: not the host gateway", u, line)
			}
			if strings.Contains(string(b), "\nPod=") {
				t.Errorf("%s: %s: a container in a pod cannot carry AddHost; the .pod does", u, line)
			}
		}
	}
}
