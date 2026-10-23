package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

// TestMain lets this test binary stand in for laib when a program behind the
// verb door calls back (LAIB_KERNEL is os.Executable, ADR 0026).
func TestMain(m *testing.M) {
	if os.Getenv("LAIB_TEST_AS_LAIB") == "1" {
		os.Exit(runArgs(os.Args[1:]))
	}
	os.Setenv("LAIB_TEST_AS_LAIB", "1")
	code := m.Run()
	if haushaltDir != "" {
		os.RemoveAll(haushaltDir)
	}
	os.Exit(code)
}

var (
	haushaltOnce sync.Once
	haushaltDir  string
	haushaltErr  error
)

// withHaushalt builds laib-haushalt once and serves the door's verbs with it.
func withHaushalt(t *testing.T) {
	t.Helper()
	haushaltOnce.Do(func() {
		if haushaltDir, haushaltErr = os.MkdirTemp("", "laib-haushalt"); haushaltErr != nil {
			return
		}
		out, err := exec.Command("go", "build", "-o", filepath.Join(haushaltDir, "laib-haushalt"), "../../haushalt/cmd/laib-haushalt").CombinedOutput()
		if err != nil {
			haushaltErr = &buildErr{err, string(out)}
		}
	})
	if haushaltErr != nil {
		t.Fatalf("go build laib-haushalt: %v", haushaltErr)
	}
	t.Setenv("LAIB_HAUSHALT", filepath.Join(haushaltDir, "laib-haushalt"))
}

type buildErr struct {
	err error
	out string
}

func (b *buildErr) Error() string { return b.err.Error() + ": " + b.out }
