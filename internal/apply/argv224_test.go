package apply

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/plan"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// #224: a secret reaches its container as a podman secret, its value on
// podman's stdin, never on any argv (ps shows argv to every user on the box,
// and Quadlet puts every Environment= value there). A fake podman and a fake
// systemctl on PATH log every argv and keep the secrets podman was handed.
// A new value replaces the secret and restarts the service's unit; a removed
// service takes its secrets with it.
func TestSecretsReachContainersOffArgv(t *testing.T) {
	home, bin := t.TempDir(), t.TempDir()
	store, log := filepath.Join(bin, "store"), filepath.Join(bin, "argv.log")
	os.MkdirAll(store, 0o755)
	fakes := map[string]string{
		"systemctl": `echo "systemctl $*" >> "$ARGV_LOG"; case "$*" in *is-active*) echo active;; esac`,
		"podman": `echo "podman $*" >> "$ARGV_LOG"
case "$1 $2" in
"secret create") for a; do n=$prev; prev=$a; done; cat > "$STORE/$n";;
"secret ls") ls "$STORE";;
"secret inspect") shift 5; for n; do printf '%s "%s"\n' "$n" "$(cat "$STORE/$n")"; done;;
"secret rm") rm "$STORE/$3";;
esac`,
	}
	for name, body := range fakes {
		os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("ARGV_LOG", log)
	t.Setenv("STORE", store)
	real := sysd.Run
	t.Cleanup(func() { sysd.Run = real })
	sysd.Run = unswappedRun // the real thing: an earlier test may have swapped it

	paths := boxfs.Paths{Home: home, State: filepath.Join(home, "state"), Quadlet: filepath.Join(home, "q"), Render: filepath.Join(home, "r"), Systemd: filepath.Join(home, "s")}
	os.MkdirAll(paths.State, 0o755)
	os.WriteFile(filepath.Join(paths.State, "box.yml"), []byte("domain: x.home\n"), 0o644) // a repo with a first commit
	st := &state.State{Box: state.Box{Domain: "x.home", DataDir: filepath.Join(home, "d")}, Services: map[string]*state.Service{
		"vault": {Name: "vault", Dir: t.TempDir(), Values: map[string]state.Value{"TOKEN": {Secret: "VAULT_TOKEN"}, "LEVEL": {Literal: "3"}},
			Contract: state.Contract{Secrets: []state.Secret{{Name: "VAULT_TOKEN"}}},
			Units:    map[string]string{"vault.container": "[Container]\nImage=x/vault:1\nEnvironment=LEVEL=${LEVEL}\nEnvironment=\"AUTH=Bearer ${TOKEN}\"\n\n[Install]\nWantedBy=default.target\n"}},
	}}
	apply := func(secrets map[string]string) Result {
		t.Helper()
		os.Remove(log)
		p, err := plan.Compute(context.Background(), st, secrets, paths, "")
		if err != nil {
			t.Fatal(err)
		}
		res, err := Run(context.Background(), st, p, paths, secrets, "test")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(log)
		for _, v := range secrets {
			if strings.Contains(string(b), v) {
				t.Fatalf("a secret value went onto an argv, where ps shows it:\n%s", b)
			}
		}
		unit, _ := os.ReadFile(filepath.Join(paths.Quadlet, "vault", "vault.container"))
		for _, v := range secrets {
			if strings.Contains(string(unit), v) {
				t.Fatalf("the unit carries a secret value, which Quadlet puts on podman run's argv:\n%s", unit)
			}
		}
		return res
	}
	held := func() map[string]string {
		got := map[string]string{}
		entries, _ := os.ReadDir(store)
		for _, e := range entries {
			b, _ := os.ReadFile(filepath.Join(store, e.Name()))
			got[e.Name()] = string(b)
		}
		return got
	}
	restarted := func(res Result) bool { return strings.Contains(strings.Join(res.Restarted, " "), "vault.service") }

	apply(map[string]string{"VAULT_TOKEN": "s3cr3t-first-224"})
	got := held()
	if len(got) != 1 {
		t.Fatalf("one podman secret for the one secret-backed line, got %v", got)
	}
	for name, v := range got {
		if v != "Bearer s3cr3t-first-224" || !strings.HasPrefix(name, "laib.vault.") {
			t.Fatalf("the podman secret must hold the line's value and be named for its service: %s=%q", name, v)
		}
	}
	if res := apply(map[string]string{"VAULT_TOKEN": "s3cr3t-first-224"}); len(res.Written) != 0 || restarted(res) {
		t.Fatalf("the same value again changes nothing and restarts nothing: %+v", res)
	}
	res := apply(map[string]string{"VAULT_TOKEN": "s3cr3t-rotated-224"})
	if !restarted(res) {
		t.Fatalf("a new secret value must restart the unit that reads it: %+v", res)
	}
	for _, v := range held() {
		if v != "Bearer s3cr3t-rotated-224" {
			t.Fatalf("the podman secret still holds the old value: %q", v)
		}
	}
	// a cut between the secret and the restart (#39): the next plan still sees the box differ
	for name := range held() {
		os.WriteFile(filepath.Join(store, name), []byte("stale"), 0o600)
	}
	if res := apply(map[string]string{"VAULT_TOKEN": "s3cr3t-rotated-224"}); !restarted(res) || held()[firstName(held())] != "Bearer s3cr3t-rotated-224" {
		t.Fatalf("a podman secret that differs from the state is healed and its unit restarted: %+v %v", res, held())
	}
	delete(st.Services, "vault")
	apply(map[string]string{"VAULT_TOKEN": "s3cr3t-rotated-224"})
	if got := held(); len(got) != 0 {
		t.Fatalf("a removed service's podman secrets must go with it: %v", got)
	}
}

// unswappedRun is sysd.Run as the package has it, before any test swaps it.
var unswappedRun = sysd.Run

func firstName(m map[string]string) string {
	for k := range m {
		return k
	}
	return ""
}
