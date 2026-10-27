package stick

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #801: the scan container is core's (rootless) and a USB node is root's
// 0664, so libusb cannot open the fi-6130 for use. The box's own config
// makes that one scanner core's, matched by vendor and product (its node
// changes with every power cycle); every other USB node stays root's. It
// is --box-config's, so a box already in use gets it with the next install.
func TestBoxConfigMakesTheUSBScannerCores(t *testing.T) {
	etc := boxConfigTo(t, false)
	b, err := os.ReadFile(filepath.Join(etc, "udev", "rules.d", "99-laib-scanner.rules"))
	if err != nil {
		t.Fatalf("no scanner rule: %v", err)
	}
	rule := ""
	for _, l := range strings.Split(string(b), "\n") {
		if l != "" && !strings.HasPrefix(l, "#") {
			if rule != "" {
				t.Fatalf("more than one rule:\n%s", b)
			}
			rule = l
		}
	}
	for _, want := range []string{`SUBSYSTEM=="usb"`, `ENV{DEVTYPE}=="usb_device"`, `ATTR{idVendor}=="04c5"`, `ATTR{idProduct}=="114f"`, `OWNER="core"`} {
		if !strings.Contains(rule, want) {
			t.Errorf("the rule lacks %s: %s", want, rule)
		}
	}
	if strings.Contains(rule, "MODE") {
		t.Errorf("the scanner is core's, not everybody's: %s", rule)
	}
}
