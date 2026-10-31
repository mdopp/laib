package main

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestSigtermExitsZero: a planned stop is no failure (#956). The built
// binary, sent SIGTERM once it listens, exits 0 within the limit.
func TestSigtermExitsZero(t *testing.T) {
	sigterm(t, "PERSONA_LISTEN", "PERSONAS_DIR="+t.TempDir(), "PERSONA_DATA="+t.TempDir())
}

// sigterm builds this program, starts it with env and listen (its
// address variable) on a free port, waits until it listens, sends SIGTERM and wants exit 0 within 8 s.
func sigterm(t *testing.T, listen string, env ...string) {
	bin := filepath.Join(t.TempDir(), "bin")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	cmd := exec.Command(bin)
	cmd.Env = append(append(os.Environ(), env...), listen+"="+addr)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			cmd.Process.Kill()
			t.Fatalf("not listening on %s: %v", addr, <-done)
		}
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
