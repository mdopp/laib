package main

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/token"
)

// TestMain lets this test binary stand in for laib when a test runs it as
// the CLI (LAIB_TEST_AS_LAIB).
func TestMain(m *testing.M) {
	if os.Getenv("LAIB_TEST_AS_LAIB") == "1" {
		os.Exit(runArgs(os.Args[1:]))
	}
	os.Setenv("LAIB_TEST_AS_LAIB", "1")
	// git's automatic gc and maintenance repack a test's state repo in the
	// background, mid-test: a watcher then sees files change that laib never
	// touched, and TempDir's cleanup finds .git not empty
	os.Setenv("GIT_CONFIG_COUNT", "2")
	os.Setenv("GIT_CONFIG_KEY_0", "gc.auto")
	os.Setenv("GIT_CONFIG_VALUE_0", "0")
	os.Setenv("GIT_CONFIG_KEY_1", "maintenance.auto")
	os.Setenv("GIT_CONFIG_VALUE_1", "false")
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

// doorKey puts the door's key into the box's secrets.age, as apply
// generates it, and returns it.
func doorKey(t *testing.T, env *cliEnv) string {
	t.Helper()
	sec, err := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 16)
	rand.Read(b)
	sec[render.HaushaltKey] = hex.EncodeToString(b)
	if err := secrets.Save(env.Paths.Secrets, env.Paths.Key, sec); err != nil {
		t.Fatal(err)
	}
	return sec[render.HaushaltKey]
}

// withHaushalt runs laib-haushalt as its container does (ADR 0027): the
// program, built once, listening on 127.0.0.1 with the door's key, and
// calling laib back over MCP with a token of the haushalt hand. The door
// reaches it at LAIB_HAUSHALT_URL.
func withHaushalt(t *testing.T, env *cliEnv) {
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
	key := doorKey(t, env)
	store, err := token.Load(cli.TokenPath(env))
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := store.Mint("haushalt", 0, "test")
	if err != nil {
		t.Fatal(err)
	}
	laib := httptest.NewServer(mcp.Handler(env))
	t.Cleanup(laib.Close)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	cmd := exec.Command(filepath.Join(haushaltDir, "laib-haushalt"))
	cmd.Env = append(os.Environ(), "HAUSHALT_LISTEN="+addr, "HAUSHALT_KEY="+key, "LAIB_MCP_URL="+laib.URL+"/mcp", "LAIB_TOKEN="+tok)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	for i := 0; ; i++ {
		if resp, err := http.Get("http://" + addr + "/health"); err == nil {
			resp.Body.Close()
			break
		}
		if i > 100 {
			t.Fatal("laib-haushalt does not answer")
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Setenv("LAIB_HAUSHALT_URL", "http://"+addr)
}

type buildErr struct {
	err error
	out string
}

func (b *buildErr) Error() string { return b.err.Error() + ": " + b.out }
