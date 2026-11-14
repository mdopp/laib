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
