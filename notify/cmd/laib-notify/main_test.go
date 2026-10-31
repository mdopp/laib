package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestSigtermExitsZero: a stop during a pass is no failure (#956). The
// built binary, sent SIGTERM while laib's /ready hangs, ends its pass and
// exits 0 within the limit, as at its own 4-minute limit.
func TestSigtermExitsZero(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "bin")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	asked := make(chan bool, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked <- true
		<-r.Context().Done()
	}))
	defer srv.Close()
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "LAIB_READY_URL="+srv.URL, "WATCHDOG_URL="+srv.URL, "NOTIFY_HOME="+t.TempDir())
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-asked:
	case err := <-done:
		t.Fatalf("ended before it asked /ready: %v", err)
	}
	cmd.Process.Signal(syscall.SIGTERM)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("SIGTERM: %v, want exit 0", err)
		}
	case <-time.After(8 * time.Second):
		cmd.Process.Kill()
		t.Fatal("SIGTERM: still running after 8 s")
	}
}
