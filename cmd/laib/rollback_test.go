package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/token"
)

// a registry template on the box, as laib add finds it
func writeRegistryTemplate(t *testing.T, c *cbox, name string) {
	t.Helper()
	dir := filepath.Join(c.env.Paths.Home, "registries", "t", "templates", name)
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "laib.yml"), []byte("name: "+name+"\ntemplate: {registry: t, name: "+name+", version: 1}\nroutes: []\nsecrets:\n  - name: "+strings.ToUpper(name)+"_KEY\ndata: []\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "values.example.yml"), []byte("IMAGE: docker.io/library/busybox:stable\n"), 0o644)
	os.WriteFile(filepath.Join(dir, name+".container"), []byte("[Container]\nImage=${IMAGE}\n\n[Install]\nWantedBy=default.target\n"), 0o644)
}

// #41 (1, 2, 3): right after an approve (a merge commit) rollback by
// proposal works, removes the added service's files and units, and leaves
// the other secrets untouched.
func TestRollbackAfterAnApproveRemovesTheAddedService(t *testing.T) {
	c := newCbox(t)
	writeRegistryTemplate(t, c, "echo2")
	if rc := inbox.Add(context.Background(), c.env, []string{"echo2"}); rc != cli.ExitOK {
		t.Fatalf("add rc %d", rc)
	}
	if _, err := os.Stat(filepath.Join(c.env.Paths.Quadlet, "echo2", "echo2.container")); err != nil {
		t.Fatal("the added service's unit is not on the box")
	}
	before, _ := secrets.Load(c.env.Paths.Secrets, c.env.Paths.Key)
	if before["ECHO2_KEY"] == "" || before["DNS_API_TOKEN"] == "" {
		t.Fatalf("fixture: %v", before)
	}
	ps, _ := proposal.List(cli.InboxDir(c.env), false)
	id := ps[0].ID
	c.calls = nil
	if rc := inbox.Rollback(context.Background(), c.env, []string{"#" + itoa(id)}); rc != cli.ExitOK {
		t.Fatalf("rollback #%d rc %d", id, rc)
	}
	if _, err := os.Stat(filepath.Join(c.env.Paths.State, "services", "echo2")); err == nil {
		t.Fatal("echo2 still in the state after the rollback")
	}
	if _, err := os.Stat(filepath.Join(c.env.Paths.Quadlet, "echo2")); err == nil {
		t.Fatal("echo2's unit files still on the box: a removed service keeps running")
	}
	joined := strings.Join(c.calls, "\n")
	if !strings.Contains(joined, "systemctl --user stop echo2.service") {
		t.Fatalf("echo2.service not stopped:\n%s", joined)
	}
	after, _ := secrets.Load(c.env.Paths.Secrets, c.env.Paths.Key)
	if after["DNS_API_TOKEN"] != before["DNS_API_TOKEN"] || after["WHOAMI_TOKEN"] != before["WHOAMI_TOKEN"] {
		t.Fatal("other services' secrets changed with the rollback")
	}
	if _, ok := after["ECHO2_KEY"]; ok {
		t.Fatal("the removed service's secret stayed")
	}
	if out, _ := runGitCmd(t, "-C", c.env.Paths.State, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Fatalf("state repo not clean:\n%s", out)
	}
}

// #41 (4): the commit argument is validated, never an option for git.
func TestRollbackRefusesAnOptionAsCommit(t *testing.T) {
	c := newCbox(t)
	out := filepath.Join(t.TempDir(), "x")
	if rc := inbox.Rollback(context.Background(), c.env, []string{"--output=" + out}); rc == cli.ExitOK {
		t.Fatal("an option was accepted as a commit")
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("git wrote the file: option injection")
	}
	if rc := inbox.Rollback(context.Background(), c.env, []string{"no-such-commit"}); rc == cli.ExitOK {
		t.Fatal("an unknown commit was accepted")
	}
}

// #41 (3): operator disable stops the unit, removes its file and revokes
// the ops token — nothing keeps running with a valid token.
func TestOperatorDisableStopsTheUnitAndRevokesTheToken(t *testing.T) {
	c := newCbox(t)
	b, _ := os.ReadFile(filepath.Join(c.env.Paths.State, "box.yml"))
	os.WriteFile(filepath.Join(c.env.Paths.State, "box.yml"), append(b, []byte("model:\n  url: http://10.0.0.9:11435/v1\n")...), 0o644)
	if rc := cmdOperator(context.Background(), c.env, []string{"enable"}); rc != cli.ExitOK {
		t.Fatalf("enable rc %d", rc)
	}
	unit := filepath.Join(c.env.Paths.Systemd, "laib-operator.service")
	if _, err := os.Stat(unit); err != nil {
		t.Fatal("operator unit not rendered")
	}
	store, _ := token.Load(cli.TokenPath(c.env))
	var ops int
	for _, r := range store.List() {
		if r.Hand == "ops" {
			ops++
		}
	}
	if ops == 0 {
		t.Fatal("no ops token minted")
	}
	c.calls = nil
	if rc := cmdOperator(context.Background(), c.env, []string{"disable"}); rc != cli.ExitOK {
		t.Fatalf("disable rc %d", rc)
	}
	if _, err := os.Stat(unit); err == nil {
		t.Fatal("the operator unit file is still there")
	}
	joined := strings.Join(c.calls, "\n")
	if !strings.Contains(joined, "stop laib-operator.service") || !strings.Contains(joined, "disable laib-operator.service") {
		t.Fatalf("the unit must be stopped and disabled:\n%s", joined)
	}
	store, _ = token.Load(cli.TokenPath(c.env))
	for _, r := range store.List() {
		if r.Hand == "ops" {
			t.Fatal("the ops token is still valid after disable")
		}
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

// #50 (2): removing a service another one names in after: is refused in
// plain words, before anything is stopped or deleted.
func TestRemoveRefusesWhenAnotherServiceDependsOnIt(t *testing.T) {
	c := newCbox(t)
	dir := filepath.Join(c.env.Paths.Home, "registries", "t", "templates", "dep")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "laib.yml"), []byte("name: dep\ntemplate: {registry: t, name: dep, version: 1}\nafter: [whoami]\nroutes: []\nsecrets: []\ndata: []\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "values.example.yml"), []byte("IMAGE: docker.io/library/busybox:stable\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "dep.container"), []byte("[Container]\nImage=${IMAGE}\n\n[Install]\nWantedBy=default.target\n"), 0o644)
	if rc := inbox.Add(context.Background(), c.env, []string{"dep"}); rc != cli.ExitOK {
		t.Fatalf("add rc %d", rc)
	}
	c.calls = nil
	if rc := cmdRemove(context.Background(), c.env, []string{"whoami"}); rc == cli.ExitOK {
		t.Fatal("whoami removed although dep needs it")
	}
	if strings.Contains(strings.Join(c.calls, "\n"), "systemctl --user stop") {
		t.Fatalf("something was stopped before the refusal:\n%s", strings.Join(c.calls, "\n"))
	}
	if _, err := os.Stat(filepath.Join(c.env.Paths.State, "services", "whoami")); err != nil {
		t.Fatal("whoami was deleted from the state")
	}
	if st, err := cli.LoadState(c.env); err != nil || st.Services["whoami"] == nil {
		t.Fatalf("the state must still load with whoami: %v", err)
	}
}
