package backup

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A data path a template declares but has not created yet is left out of
// the snapshot, not read as a failure (2026-10-27: paperless' first update
// with dokumente's folder went red and was rolled back).
func TestADeclaredPathThatDoesNotExistYetIsLeftOut(t *testing.T) {
	dir := t.TempDir()
	there := filepath.Join(dir, "paperless")
	if err := os.Mkdir(there, 0o700); err != nil {
		t.Fatal(err)
	}
	got := present([]string{there, filepath.Join(dir, "dokumente")})
	if !slices.Equal(got, []string{there}) {
		t.Fatalf("present = %v, want only %s", got, there)
	}
	if got := present([]string{filepath.Join(dir, "nichts")}); len(got) != 0 {
		t.Fatalf("present = %v, want none", got)
	}
}
