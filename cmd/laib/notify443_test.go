package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/state"
)

// #443 (second cut): the owner's mail (#393) and the outside watchdog ping
// (#57) left laib serve for the program laib-notify. The kernel gives it a
// narrow hand, notify (diag, updates, note: it reads, and journals a failed
// send; it decides nothing), mints its token on apply and renders its unit
// and five-minute timer, only when there is something to do: a watchdog
// URL, or Authelia's mail settings and a recipient.
func TestTheNotifierRunsAsItsOwnProgramWithANarrowHand(t *testing.T) {
	// laib init writes the hand; a policy.yml from before has it from Load
	home := t.TempDir()
	env := &cliEnv{Paths: newRbox(t).env.Paths}
	env.Paths.State, env.Actor = filepath.Join(home, "state"), "human:test"
	env.Paths.Secrets, env.Paths.Key = filepath.Join(home, "state", "secrets.age"), filepath.Join(home, "age.key")
	if rc := cmdInit(context.Background(), env, []string{"--domain", "n.home", "--data-dir", filepath.Join(home, "data")}); rc != cli.ExitOK {
		t.Fatalf("init rc %d", rc)
	}
	pol, _ := os.ReadFile(filepath.Join(env.Paths.State, "policy.yml"))
	if !strings.Contains(string(pol), "notify: {ceiling: propose, verbs: [diag, updates, inbox, note]}") {
		t.Errorf("laib init writes no notify hand:\n%s", pol)
	}

	b := newRbox(t) // the golden policy.yml, from before the hand
	st, err := cli.LoadState(b.env)
	if err != nil {
		t.Fatal(err)
	}
	var tools []string
	for _, tl := range mcp.Tools(st, "notify") {
		tools = append(tools, tl["name"].(string))
	}
	slices.Sort(tools)
	if strings.Join(tools, " ") != "laib_diag laib_inbox laib_note laib_updates" {
		t.Fatalf("the notify hand reaches %v, want diag, inbox (#441), note, updates and nothing else", tools)
	}
	if out, _ := render.Render(st, nil); out["quadlet/laib-notify/laib-notify.container"] != "" {
		t.Error("a box without a watchdog and without mail renders a notifier")
	}

	// a watchdog and Authelia's mail settings
	box := filepath.Join(b.env.Paths.State, "box.yml")
	raw, _ := os.ReadFile(box)
	os.WriteFile(box, append(raw, []byte("watchdog:\n  url: https://hc.example/ping/abc\n")...), 0o644)
	vals := filepath.Join(b.env.Paths.State, "services", "auth", "values.yml")
	raw, _ = os.ReadFile(vals)
	os.WriteFile(vals, append(raw, []byte("SMTP_HOST: smtp.example\nSMTP_PORT: \"587\"\nSMTP_SECURITY: starttls\nSMTP_USER: box@example\nSMTP_FROM: Die Box <box@example>\n")...), 0o644)
	if st, err = cli.LoadState(b.env); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(render.SecretNames(st), state.Secret{Name: render.NotifyToken, External: true, Mint: "notify"}) {
		t.Errorf("apply does not mint the notifier's token: %v", render.SecretNames(st))
	}
	sec := map[string]string{render.NotifyToken: "tok-n", "AUTHELIA_SMTP_PASSWORD": `p%w"x`}
	out, err := render.Render(st, sec)
	if err != nil {
		t.Fatal(err)
	}
	// its container since ADR 0027 (#624): the image, the secrets as podman secrets
	unit, timer, pod := out["quadlet/laib-notify/laib-notify.container"], out["systemd/laib-notify.timer"], render.PodmanSecrets(st, sec)
	for _, w := range []string{"Image=" + render.NotifyImage, "Secret=laib.laib-notify.laib-notify.SMTP_PASSWORD,type=env,target=SMTP_PASSWORD", `Environment="WATCHDOG_URL=https://hc.example/ping/abc"`,
		`Environment="SMTP_FROM=Die Box <box@example>"`, `Environment="MAIL_TO=admin@example.home"`, "/ready", "/mcp"} {
		if !strings.Contains(unit, w) {
			t.Errorf("the notifier's unit lacks %s:\n%s", w, unit)
		}
	}
	// the secrets reach podman on stdin, never the unit
	if strings.Contains(unit, "tok-n") || strings.Contains(unit, "p%w") || pod["laib.laib-notify.laib-notify.LAIB_TOKEN"] != "tok-n" || pod["laib.laib-notify.laib-notify.SMTP_PASSWORD"] != `p%w"x` {
		t.Errorf("unit:\n%s\npodman secrets: %v", unit, pod)
	}
	if !strings.Contains(timer, "OnUnitActiveSec=5m") {
		t.Errorf("not every five minutes:\n%s", timer)
	}
}
