package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/boxfs"
)

// box.yml from flags, not from `echo >>` in a runbook (#45): the DNS
// provider for DNS-01 and the TLS mode are init's business.
func TestInitWritesDNSProviderAndTLSMode(t *testing.T) {
	home := t.TempDir()
	paths := boxfs.Paths{Home: home, State: filepath.Join(home, "state"), Journal: filepath.Join(home, "journal"), Render: filepath.Join(home, "render"), Key: filepath.Join(home, "age.key"), Secrets: filepath.Join(home, "state", "secrets.age"), Lock: filepath.Join(home, "lock")}
	env := &cliEnv{paths: paths, actor: "human:test"}
	if rc := cmdInit(context.Background(), env, []string{"--domain", "nuc.example", "--dns-provider", "desec"}); rc != exitOK {
		t.Fatalf("rc %d", rc)
	}
	b, _ := os.ReadFile(filepath.Join(paths.State, "box.yml"))
	if !strings.Contains(string(b), "dns:\n  provider: desec\n  secretRef: DNS_API_TOKEN\n") {
		t.Fatalf("no dns provider in box.yml:\n%s", b)
	}
	if strings.Contains(string(b), "tls:") {
		t.Fatal("no --tls given: box.yml must not pin a TLS mode (Let's Encrypt is the default)")
	}
	home2 := t.TempDir()
	paths.Home, paths.State, paths.Key, paths.Secrets = home2, filepath.Join(home2, "state"), filepath.Join(home2, "age.key"), filepath.Join(home2, "state", "secrets.age")
	if rc := cmdInit(context.Background(), &cliEnv{paths: paths, actor: "human:test"}, []string{"--domain", "lab.example", "--tls", "internal"}); rc != exitOK {
		t.Fatalf("rc %d", rc)
	}
	b, _ = os.ReadFile(filepath.Join(paths.State, "box.yml"))
	if !strings.Contains(string(b), "tls: internal") || strings.Contains(string(b), "dns:") {
		t.Fatalf("tls internal without a dns block expected:\n%s", b)
	}
	if rc := cmdInit(context.Background(), &cliEnv{paths: paths, actor: "human:test"}, []string{"--domain", "x", "--tls", "weird"}); rc == exitOK {
		t.Fatal("an unknown TLS mode must be refused")
	}
}
