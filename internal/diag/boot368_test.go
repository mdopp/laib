package diag

import (
	"strings"
	"testing"
)

// #368: /boot on the Kraftwerk is 350 MiB with 33 MiB free and one kernel
// set is ~147 MiB: the next OS update does not fit. Measured, not assumed:
// diag says so before Zincati finds out.
func TestBootWithoutRoomForAKernelSetIsYellow(t *testing.T) {
	const mib = 1 << 20
	if f := bootFinding(33*mib, 350*mib); f == nil || f.Severity != "gelb" || f.Probe != "boot" || !strings.Contains(f.Detail, "33 MiB") {
		t.Fatalf("33 MiB free on /boot: %+v", f)
	}
	// #949: the box removes the rollback itself once the booted deployment is proven
	if f := bootFinding(33*mib, 350*mib); !strings.Contains(f.Fix, "nach dem nächsten bewiesenen Start") || strings.Contains(f.Fix, "sudo ") {
		t.Fatalf("the fix must say the box frees /boot itself, not hand the owner a command: %q", f.Fix)
	}
	if f := bootFinding(200*mib, 350*mib); f != nil {
		t.Fatalf("200 MiB free fits a kernel set: %+v", f)
	}
	if f := bootFinding(0, 0); f != nil {
		t.Fatalf("no /boot (not mounted): %+v", f)
	}
}
