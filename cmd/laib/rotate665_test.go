package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/sysd"
)

// #665: the passwords carried from ServiceBay are data-bound (#353): the app
// keeps its own copy, so a new value in secrets.age alone locks it out. A
// template whose hook can change it inside the app marks it `rotate: true`:
// `laib secret rotate` then keeps the new value aside, hands it to that
// hook as LAIB_ROTATE_<NAME>, and takes it only when the hook hands it back
// (the app took it). Without the hand-back the old value stays; a red apply
// after the change rotates back, once. No value is printed or journaled.

// rotatingAuth marks auth's LLDAP_ADMIN_PASSWORD data-bound and rotatable,
// gives auth a post-deploy and sets the value the data was made with.
func rotatingAuth(t *testing.T, b *rbox) {
	t.Helper()
	dir := filepath.Join(b.env.Paths.State, "services", "auth")
	raw, _ := os.ReadFile(filepath.Join(dir, "laib.yml"))
	text := strings.Replace(string(raw), "  - name: LLDAP_ADMIN_PASSWORD\n", "  - name: LLDAP_ADMIN_PASSWORD\n    dataBound: true\n    rotate: true\n", 1)
	os.WriteFile(filepath.Join(dir, "laib.yml"), []byte(text), 0o644)
	os.WriteFile(filepath.Join(dir, "post-deploy"), []byte("#!/bin/sh\n"), 0o755)
	sec, _ := secrets.Load(b.env.Paths.Secrets, b.env.Paths.Key)
	sec["LLDAP_ADMIN_PASSWORD"] = "made-the-data-665"
	sec["DNS_API_TOKEN"] = "dns-token-665" // rbox's "x" would redact every laib-exec line into a refusal
	secrets.Save(b.env.Paths.Secrets, b.env.Paths.Key, sec)
}

// hookEnv reads one variable of a hook's env file.
func hookEnv(h apply.HookSpec, name string) string {
	b, _ := os.ReadFile(h.EnvFile)
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, name+"="); ok {
			return v
		}
	}
	return ""
}

// fakeApp is an app that keeps its own copy of the password: the hook
// changes it only from the right current value, as LDAP's modify would.
type fakeApp struct {
	pw      string
	failRed int // this many runs that took a new value end red afterwards
	exec    string
}

func (a *fakeApp) hook(_ context.Context, h apply.HookSpec) (string, error) {
	cur, next := hookEnv(h, "LAIB_SECRET_LLDAP_ADMIN_PASSWORD"), hookEnv(h, "LAIB_ROTATE_LLDAP_ADMIN_PASSWORD")
	if next == "" || next == cur {
		return "seeded\n", nil
	}
	if cur != a.pw {
		return "LLDAP: admin cannot log in\n", errors.New("exit status 1")
	}
	out := a.exec
	if a.exec == "" {
		a.pw = next
	}
	out += "laib-secret: LLDAP_ADMIN_PASSWORD=" + next + "\n"
	if a.failRed > 0 {
		a.failRed--
		return out + "something else went wrong\n", errors.New("exit status 1")
	}
	return out, nil
}

func rotateLLDAP(t *testing.T, b *rbox, a *fakeApp) (rc int, out string) {
	t.Helper()
	realHook := apply.RunHook
	t.Cleanup(func() { apply.RunHook = realHook })
	apply.RunHook = a.hook
	var w bytes.Buffer
	b.env.Out, b.env.ErrOut = &w, &w
	rc = cmdSecret(context.Background(), b.env, []string{"rotate", "LLDAP_ADMIN_PASSWORD"})
	return rc, w.String()
}

func noValueAnywhere(t *testing.T, b *rbox, out string, values ...string) {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(b.env.Paths.Journal, "*"))
	for _, v := range values {
		if strings.Contains(out, v) {
			t.Fatalf("a password in the output: %q", out)
		}
		for _, f := range files {
			if j, _ := os.ReadFile(f); strings.Contains(string(j), v) {
				t.Fatalf("a password in the journal %s", f)
			}
		}
	}
}

func TestRotateInTheAppTakesTheValueTheHookHandsBack(t *testing.T) {
	b := newRbox(t)
	rotatingAuth(t, b)
	app := &fakeApp{pw: "made-the-data-665"}
	rc, out := rotateLLDAP(t, b, app)
	if rc != cli.ExitOK {
		t.Fatalf("rotate in the app: rc %d: %s", rc, out)
	}
	sec, _ := secrets.Load(b.env.Paths.Secrets, b.env.Paths.Key)
	if sec["LLDAP_ADMIN_PASSWORD"] == "made-the-data-665" || sec["LLDAP_ADMIN_PASSWORD"] != app.pw {
		t.Fatal("secrets.age and the app do not hold the same new value")
	}
	if _, left := sec["LLDAP_ADMIN_PASSWORD.next"]; left {
		t.Fatal("the pending value stays after the rotation")
	}
	noValueAnywhere(t, b, out, app.pw, "made-the-data-665")
}

func TestRotateInTheAppKeepsTheOldValueWhenTheAppRefuses(t *testing.T) {
	b := newRbox(t)
	rotatingAuth(t, b)
	app := &fakeApp{pw: "changed-in-the-app"} // the owner changed it in LLDAP: the hook cannot log in
	rc, out := rotateLLDAP(t, b, app)
	if rc == cli.ExitOK {
		t.Fatal("a rotation the app refused ended green")
	}
	sec, _ := secrets.Load(b.env.Paths.Secrets, b.env.Paths.Key)
	if sec["LLDAP_ADMIN_PASSWORD"] != "made-the-data-665" {
		t.Fatal("the value changed though the app did not take it")
	}
	if _, left := sec["LLDAP_ADMIN_PASSWORD.next"]; left {
		t.Fatal("a refused rotation leaves its pending value for the next apply")
	}
	if !strings.Contains(out, "LLDAP_ADMIN_PASSWORD") || !strings.Contains(out, "alte") {
		t.Fatalf("the refusal says the old value stays: %q", out)
	}
}

// the hook changes the value through a command in the app's container
// (Postgres: ALTER ROLE); when that command fails, its hand-back is not taken
func TestRotateInTheAppDropsAHandBackAfterAFailedExec(t *testing.T) {
	b := newRbox(t)
	rotatingAuth(t, b)
	prev := sysd.Run
	t.Cleanup(func() { sysd.Run = prev })
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "podman" && len(args) > 0 && args[0] == "exec" {
			return "ERROR: role does not exist", errors.New("exit status 3")
		}
		return prev(ctx, name, args...)
	}
	app := &fakeApp{pw: "made-the-data-665", exec: "laib-exec: systemd-auth-lldap psql -f /data/.laib-rotate.sql\n"}
	rc, out := rotateLLDAP(t, b, app)
	if rc == cli.ExitOK {
		t.Fatal("a failed ALTER ended green")
	}
	sec, _ := secrets.Load(b.env.Paths.Secrets, b.env.Paths.Key)
	if sec["LLDAP_ADMIN_PASSWORD"] != "made-the-data-665" {
		t.Fatalf("the hand-back after a failed exec was taken: the app is locked out: %s", out)
	}
	if !strings.Contains(out, "nicht übernommen") {
		t.Fatalf("the dropped hand-back is not said: %s", out)
	}
}

func TestRotateRollsBackOnceWhenRedAfterTheChange(t *testing.T) {
	b := newRbox(t)
	rotatingAuth(t, b)
	app := &fakeApp{pw: "made-the-data-665", failRed: 1}
	rc, out := rotateLLDAP(t, b, app)
	if rc == cli.ExitOK {
		t.Fatal("red after the change ended green")
	}
	sec, _ := secrets.Load(b.env.Paths.Secrets, b.env.Paths.Key)
	if sec["LLDAP_ADMIN_PASSWORD"] != "made-the-data-665" || app.pw != "made-the-data-665" {
		t.Fatal("not rolled back to the old value in both the app and secrets.age")
	}
	if !strings.Contains(out, "zurück") {
		t.Fatalf("the rollback is not said: %q", out)
	}
	noValueAnywhere(t, b, out, "made-the-data-665")
}
