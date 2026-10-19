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
	return boteBoxSecrets(t, people, values, map[string]string{"BOTE_APPROVER_TOKEN": "approver-tok", "BOTE_MATRIX_TOKEN": "matrix-tok", "BOTE_TELEGRAM_TOKEN": "tg-tok", "BOTE_DISCORD_TOKEN": "dc-tok", "BOTE_HA_TOKEN": "ha-tok"})
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
	matrixOn   = map[string]string{"MATRIX_HOMESERVER": "https://hs.example.org", "MATRIX_ALLOWED_USERS": "@admin:example.org", "ADMIN_MXID": "@admin:example.org", "ADMIN_ROOM": "!raum:example.org"}
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
// each optional; an unset one leaves no container. The relay runs while an
// approval identity (Signal number, Matrix ID) is set; Telegram never approves.
func TestBoteChannelsAreAChoice(t *testing.T) {
	signal := "quadlet/bote/bote-signal.container"
	relay := "quadlet/bote/bote-relay.container"

	out, err := boteBox(t, "", map[string]string{"ADMIN_NUMBER": "+4915198765432"}) // values.example.yml: Signal only
	if err != nil {
		t.Fatal(err)
	}
	if out[signal] == "" || out[relay] == "" {
		t.Fatalf("Signal only: signal-cli and the relay run: %v", keys(out))
	}
	// the relay holds the approver token (#48): a value that names a secret renders it
	if !strings.Contains(out[relay], "LAIB_TOKEN=approver-tok\n") {
		t.Fatalf("the relay's approver token is not rendered:\n%s", out[relay])
	}

	// the example's defaults: switching Signal off and Matrix on is enough (no stray ADMIN_NUMBER)
	out, err = boteBox(t, "", merge(map[string]string{"SIGNAL_ACCOUNT": ""}, matrixOn))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out[signal]; ok {
		t.Fatal("Matrix only: no bote-signal unit")
	}
	for _, want := range []string{"ADMIN_MXID=@admin:example.org\n", "ADMIN_ROOM=!raum:example.org\n", "MATRIX_HOMESERVER=https://hs.example.org\n", "MATRIX_ACCESS_TOKEN=matrix-tok\n"} {
		if !strings.Contains(out[relay], want) {
			t.Errorf("Matrix only: the relay lacks %s:\n%s", want, out[relay])
		}
	}

	out, err = boteBox(t, "", merge(noSignal, telegramOn))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out[signal]; ok {
		t.Fatal("Telegram only: no bote-signal unit")
	}
	if _, ok := out[relay]; ok {
		t.Fatal("Telegram only: no relay, Telegram never approves")
	}

	out, err = boteBox(t, "", merge(matrixOn, telegramOn, map[string]string{"ADMIN_NUMBER": "+4915198765432"}))
	if err != nil {
		t.Fatal(err)
	}
	if out[signal] == "" || out[relay] == "" || !strings.Contains(out[relay], "ADMIN_NUMBER=+") || !strings.Contains(out[relay], "ADMIN_MXID=@admin") {
		t.Fatalf("all three: signal-cli and one relay for both identities: %v", keys(out))
	}

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

// #184 review: a channel combination the relay or the hook would refuse
// fails plan, before any unit restarts: an approval identity without its
// channel or room, and Matrix or Telegram without its token (plan knows
// secrets; lint, without them, does not ask).
func TestBoteRefusesBrokenChannelsAtPlan(t *testing.T) {
	for what, c := range map[string]struct {
		values map[string]string
		want   string
	}{
		"ADMIN_NUMBER without Signal":   {merge(noSignal, matrixOn, map[string]string{"ADMIN_NUMBER": "+4915198765432"}), "SIGNAL_ACCOUNT"},
		"ADMIN_MXID without Matrix":     {map[string]string{"ADMIN_MXID": "@admin:example.org", "ADMIN_ROOM": "!raum:example.org"}, "MATRIX_HOMESERVER"},
		"ADMIN_MXID without ADMIN_ROOM": {merge(matrixOn, map[string]string{"ADMIN_ROOM": ""}), "ADMIN_ROOM"},
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
		if _, err := boteBoxSecrets(t, "", c.values, map[string]string{"BOTE_APPROVER_TOKEN": "a"}); err == nil || !strings.Contains(err.Error(), c.want) {
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
	if !strings.Contains(out[pod], "PublishPort=127.0.0.1:8645:8645\n") || !strings.Contains(out[nft], "8645") {
		t.Fatalf("no Signal: bote-notify's port stays:\n%s\n%s", out[pod], out[nft])
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
// DISCORD_ALLOWED_USERS is set, no signal-cli and no relay for it (Discord
// never approves), a person reached on their DM channel id, and plan refuses
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
	if _, ok := out["quadlet/bote/bote-relay.container"]; ok {
		t.Fatal("Discord only: no relay, Discord never approves")
	}
	if _, err := boteBox(t, "people: {papa: [\"discord:998877665544332211\"]}\n", nil); err == nil || !strings.Contains(err.Error(), "DISCORD_ALLOWED_USERS") {
		t.Fatalf("a discord target on a Bote without Discord must fail plan: %v", err)
	}
	if _, err := boteBoxSecrets(t, "", merge(noSignal, discordOn), map[string]string{"BOTE_APPROVER_TOKEN": "a"}); err == nil || !strings.Contains(err.Error(), "BOTE_DISCORD_TOKEN") {
		t.Fatalf("Discord users without the bot token must fail plan: %v", err)
	}
	if _, err := boteBoxSecrets(t, "", nil, map[string]string{"BOTE_APPROVER_TOKEN": "a", "BOTE_DISCORD_TOKEN": "dc-tok"}); err != nil {
		t.Fatalf("a Discord bot token without users is Discord off, not an error: %v", err)
	}
}

// #463: the owner's verdicts on Discord: with ADMIN_DISCORD_ID the relay
// runs (on a Discord-only Bote too) and reads the owner's DM with the bot
// token; the owner must be on the allowed list and name the DM channel.
func TestTheRelayReadsTheOwnersDiscordDM(t *testing.T) {
	owner := map[string]string{"ADMIN_DISCORD_ID": "112233445566778899", "ADMIN_DISCORD_CHANNEL": "998877665544332211"}
	out, err := boteBox(t, "", merge(noSignal, discordOn, owner))
	if err != nil {
		t.Fatal(err)
	}
	relay := out["quadlet/bote/bote-relay.container"]
	for _, w := range []string{"ADMIN_DISCORD_ID=112233445566778899\n", "ADMIN_DISCORD_CHANNEL=998877665544332211\n", "DISCORD_BOT_TOKEN=dc-tok\n"} {
		if !strings.Contains(relay, w) {
			t.Errorf("the relay lacks %s:\n%s", w, relay)
		}
	}
	if _, err := boteBox(t, "", merge(noSignal, discordOn, map[string]string{"ADMIN_DISCORD_ID": "112233445566778899"})); err == nil || !strings.Contains(err.Error(), "ADMIN_DISCORD_CHANNEL") {
		t.Errorf("an owner id without the DM channel must fail plan: %v", err)
	}
	if _, err := boteBox(t, "", merge(map[string]string{"DISCORD_ALLOWED_USERS": ""}, owner)); err == nil || !strings.Contains(err.Error(), "DISCORD_ALLOWED_USERS") {
		t.Errorf("an owner id without Discord must fail plan: %v", err)
	}
}
