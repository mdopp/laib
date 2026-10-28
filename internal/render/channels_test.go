package render

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// boteBox renders templates/bote with values.example.yml, the given values
// replaced, and the given people (#184).
func boteBox(t *testing.T, people string, values map[string]string) (Output, error) {
	t.Helper()
	return boteBoxSecrets(t, people, values, map[string]string{"BOTE_MATRIX_TOKEN": "matrix-tok", "BOTE_TELEGRAM_TOKEN": "tg-tok", "BOTE_DISCORD_TOKEN": "dc-tok", "BOTE_HA_TOKEN": "ha-tok"})
}

func boteBoxSecrets(t *testing.T, people string, values, secrets map[string]string) (Output, error) {
	t.Helper()
	repo := t.TempDir()
	dst := filepath.Join(repo, "services", "bote")
	os.MkdirAll(dst, 0o755)
	os.WriteFile(filepath.Join(repo, "box.yml"), []byte("domain: x.home\ndataDir: /d\nroles: {}\nhostNetwork: []\nlaib: {listen: 0.0.0.0}\n"+people), 0o644)
	os.WriteFile(filepath.Join(repo, "policy.yml"), []byte("tiers: [read, propose, apply, destroy]\nhands: {human: {ceiling: destroy}}\n"), 0o644)
	files, _ := filepath.Glob(filepath.Join("..", "..", "templates", "bote", "*"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue // tests/
		}
		name := filepath.Base(f)
		if name == "values.example.yml" {
			name = "values.yml"
			for k, v := range values {
				line := regexp.MustCompile(`(?m)^` + k + `:.*$`)
				if !line.Match(b) {
					t.Fatalf("values.example.yml has no %s", k)
				}
				b = line.ReplaceAll(b, []byte(k+": \""+v+"\""))
			}
		}
		os.WriteFile(filepath.Join(dst, name), b, 0o644)
	}
	st, err := state.Load(repo)
	if err != nil {
		return nil, err
	}
	out, err := Render(st, secrets)
	return asContainerSees(st, secrets, out), err
}

var (
	noSignal   = map[string]string{"SIGNAL_ACCOUNT": "", "SIGNAL_ALLOWED_USERS": "", "ADMIN_NUMBER": ""}
	matrixOn   = map[string]string{"MATRIX_HOMESERVER": "https://hs.example.org", "MATRIX_ALLOWED_USERS": "@admin:example.org", "ADMIN_ROOM": "!raum:example.org"}
	telegramOn = map[string]string{"TELEGRAM_ALLOWED_USERS": "123456"}
	discordOn  = map[string]string{"DISCORD_ALLOWED_USERS": "112233445566778899"}
)

func merge(ms ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, m := range ms {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// #184: the Bote's channels are a choice. Signal, Matrix and Telegram are
// each optional; an unset one leaves no container. #778: no channel
// approves, so no combination renders a relay or a token that can decide.
func TestBoteChannelsAreAChoice(t *testing.T) {
	signal := "quadlet/bote/bote-signal.container"
	noRelay := func(what string, out Output) {
		t.Helper()
		for k, v := range out {
			if strings.Contains(k, "relay") || strings.Contains(v, "laib-relay") || strings.Contains(v, "APPROVER") {
				t.Errorf("%s: %s still carries the relay (#778)", what, k)
			}
		}
	}
	out, err := boteBox(t, "", map[string]string{"ADMIN_NUMBER": "+4915198765432"}) // values.example.yml: Signal only
	if err != nil {
		t.Fatal(err)
	}
	if out[signal] == "" {
		t.Fatalf("Signal only: signal-cli runs: %v", keys(out))
	}
	noRelay("Signal with the admin's number", out)

	// the example's defaults: switching Signal off and Matrix on is enough (no stray ADMIN_NUMBER)
	out, err = boteBox(t, "", merge(map[string]string{"SIGNAL_ACCOUNT": ""}, matrixOn))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out[signal]; ok {
		t.Fatal("Matrix only: no bote-signal unit")
	}
	noRelay("Matrix with the admin's room", out)

	out, err = boteBox(t, "", merge(noSignal, telegramOn))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out[signal]; ok {
		t.Fatal("Telegram only: no bote-signal unit")
	}

	out, err = boteBox(t, "", merge(matrixOn, telegramOn, map[string]string{"ADMIN_NUMBER": "+4915198765432"}))
	if err != nil {
		t.Fatal(err)
	}
	if out[signal] == "" {
		t.Fatalf("all three: signal-cli runs: %v", keys(out))
	}
	noRelay("all three", out)

	if _, err := boteBox(t, "", merge(noSignal, map[string]string{"MATRIX_HOMESERVER": "", "TELEGRAM_ALLOWED_USERS": ""})); err == nil || !strings.Contains(err.Error(), "no channel") {
		t.Fatalf("a Bote without any channel must fail plan: %v", err)
	}
}

// #184: a person's target on a channel the Bote does not have fails plan;
// on a box with that channel it is fine.
func TestBotePeopleMatchItsChannels(t *testing.T) {
	people := "people: {lena: [\"matrix:!lena:example.org\"]}\n"
	if _, err := boteBox(t, people, nil); err == nil || !strings.Contains(err.Error(), "MATRIX_HOMESERVER") {
		t.Fatalf("a matrix target on a Signal-only Bote must fail plan: %v", err)
	}
	if _, err := boteBox(t, people, matrixOn); err != nil {
		t.Fatalf("a matrix target on a Bote with Matrix: %v", err)
	}
}

// #184 review: a channel combination the hook would refuse fails plan,
// before any unit restarts: the home number without Signal, and Matrix or
// Telegram without its token (plan knows
// secrets; lint, without them, does not ask).
func TestBoteRefusesBrokenChannelsAtPlan(t *testing.T) {
	for what, c := range map[string]struct {
		values map[string]string
		want   string
	}{
		"ADMIN_NUMBER without Signal": {merge(noSignal, matrixOn, map[string]string{"ADMIN_NUMBER": "+4915198765432"}), "SIGNAL_ACCOUNT"},
	} {
		if _, err := boteBox(t, "", c.values); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: plan must fail naming %s: %v", what, c.want, err)
		}
	}
	for what, c := range map[string]struct {
		values map[string]string
		want   string
	}{
		"Matrix without its token":   {matrixOn, "BOTE_MATRIX_TOKEN"},
		"Telegram without its token": {telegramOn, "BOTE_TELEGRAM_TOKEN"},
	} {
		if _, err := boteBoxSecrets(t, "", c.values, map[string]string{"BOTE_HA_TOKEN": "a"}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: plan must fail naming %s: %v", what, c.want, err)
		}
		if _, err := boteBoxSecrets(t, "", c.values, nil); err != nil {
			t.Errorf("%s: without secrets (lint) the token is not asked: %v", what, err)
		}
	}
}

// #190: without Signal nothing about signal-cli's 8085 is published or
// declared: no PublishPort in the pod, no nftables line. With Signal it
// stays, on loopback.
func TestBoteWithoutSignalHasNoPort8085(t *testing.T) {
	pod, nft := "quadlet/bote/bote.pod", "nftables/laib.nft"
	out, err := boteBox(t, "", merge(noSignal, matrixOn))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out[pod], "8085") || strings.Contains(out[nft], "8085") {
		t.Fatalf("no Signal: 8085 is published or declared:\n%s\n%s", out[pod], out[nft])
	}
	// #894: bote-notify beyond loopback for the gesundheit pod (host.containers.internal), lan: false in nft
	if !strings.Contains(out[pod], "PublishPort=8645:8645\n") || strings.Contains(out[pod], "127.0.0.1:8645") || !strings.Contains(out[nft], "8645") {
		t.Fatalf("no Signal: bote-notify's port stays, beyond loopback:\n%s\n%s", out[pod], out[nft])
	}
	out, err = boteBox(t, "", nil) // values.example.yml: Signal only
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out[pod], "PublishPort=127.0.0.1:8085:8080\n") || !strings.Contains(out[nft], "8085") {
		t.Fatalf("Signal: 8085 stays, on loopback:\n%s\n%s", out[pod], out[nft])
	}
}

// #421: Discord is a fourth channel, as Telegram (#184): on while
// DISCORD_ALLOWED_USERS is set, no signal-cli for it, a person reached on their DM channel id, and plan refuses
// users without the bot token. A token without users is Discord off, as
// with Telegram: pre-start writes no line, Hermes starts no adapter.
func TestBoteOnDiscordOnly(t *testing.T) {
	out, err := boteBox(t, "people: {papa: [\"discord:998877665544332211\"]}\n", merge(noSignal, discordOn))
	if err != nil {
		t.Fatalf("a Discord-only Bote: %v", err)
	}
	if _, ok := out["quadlet/bote/bote-signal.container"]; ok {
		t.Fatal("Discord only: no bote-signal unit")
	}
	if _, err := boteBox(t, "people: {papa: [\"discord:998877665544332211\"]}\n", nil); err == nil || !strings.Contains(err.Error(), "DISCORD_ALLOWED_USERS") {
		t.Fatalf("a discord target on a Bote without Discord must fail plan: %v", err)
	}
	if _, err := boteBoxSecrets(t, "", merge(noSignal, discordOn), map[string]string{"BOTE_HA_TOKEN": "a"}); err == nil || !strings.Contains(err.Error(), "BOTE_DISCORD_TOKEN") {
		t.Fatalf("Discord users without the bot token must fail plan: %v", err)
	}
	if _, err := boteBoxSecrets(t, "", nil, map[string]string{"BOTE_HA_TOKEN": "a", "BOTE_DISCORD_TOKEN": "dc-tok"}); err != nil {
		t.Fatalf("a Discord bot token without users is Discord off, not an error: %v", err)
	}
}

// #469: bote-signal runs signal-cli's own HTTP daemon, which Hermes and
// bote-notify speak (/api/v1/check, /api/v1/events, /api/v1/rpc), not
// bbernhard's REST API; and pre-start gives its data dir to the image's
// user (uid 1000): a container in a pod cannot take UserNS=.
func TestBoteSignalRunsSignalCLIsDaemon(t *testing.T) {
	out, err := boteBox(t, "", map[string]string{"ADMIN_NUMBER": "+4915198765432"})
	if err != nil {
		t.Fatal(err)
	}
	unit := out["quadlet/bote/bote-signal.container"]
	for _, w := range []string{"Entrypoint=/usr/bin/signal-cli-native", "daemon --http 127.0.0.1:8080", "-a +4915112345678", "--config /home/.local/share/signal-cli"} {
		if !strings.Contains(unit, w) {
			t.Errorf("bote-signal lacks %q:\n%s", w, unit)
		}
	}
	if strings.Contains(unit, "MODE=") || strings.Contains(unit, "UserNS=") {
		t.Errorf("bote-signal still starts the REST API, or takes a user namespace a pod's container cannot:\n%s", unit)
	}
	pre, _ := os.ReadFile("../../templates/bote/pre-start")
	if !strings.Contains(string(pre), "chown -R 1000:1000") {
		t.Error("pre-start does not give the signal data dir to uid 1000")
	}
}
