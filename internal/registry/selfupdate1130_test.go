package registry

import (
	"os"
	"strings"
	"testing"
)

// #1130: self-update.sh checks the release's signature with the same pinned
// cosign the image review uses; a bump of one is a bump of both.
func TestSelfUpdateChecksWithThePinnedCosign(t *testing.T) {
	b, err := os.ReadFile("../../scripts/self-update.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `COSIGN="`+Cosign+`"`) {
		t.Fatalf("scripts/self-update.sh must pin cosign as registry.Cosign (%s)", Cosign)
	}
}

// ADR 0035: laib-updater's pre-check runs the same pinned cosign the kernel
// checks with; a bump of one is a bump of both.
func TestTheUpdaterChecksWithThePinnedCosign(t *testing.T) {
	b, err := os.ReadFile("../../updater/Containerfile")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "FROM "+Cosign+" AS cosign") {
		t.Fatalf("updater/Containerfile must take cosign from registry.Cosign (%s)", Cosign)
	}
}
