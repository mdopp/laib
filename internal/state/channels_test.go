package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chatSvc writes a service that delivers to people on signal, matrix and
// telegram, each on while its value is set (#184), with a unit per channel.
func chatSvc(t *testing.T, repo, values string) {
	t.Helper()
	c := "name: chat\ntemplate: {registry: builtin, name: chat, version: 1}\nroutes: []\nsecrets: []\ndata: []\nhealth: {none: test}\n" +
		"when:\n  chat-signal.container: [SIGNAL_ACCOUNT]\n  chat-relay.container: [ADMIN_NUMBER, ADMIN_MXID]\n" +
		"channels: {signal: SIGNAL_ACCOUNT, matrix: MATRIX_HOMESERVER, telegram: TELEGRAM_ALLOWED_USERS}\n"
	writeSvc(t, repo, "chat", c, map[string]string{
		"chat.container":        "[Container]\nImage=${IMAGE}\n",
		"chat-signal.container": "[Container]\nImage=${SIGNAL_IMAGE}\n",
		"chat-relay.container":  "[Container]\nImage=${IMAGE}\nEnvironment=ADMIN=${ADMIN_NUMBER}${ADMIN_MXID}\n",
	})
	os.WriteFile(filepath.Join(repo, "services", "chat", "values.yml"), []byte("IMAGE: img\n"+values), 0o644)
}

// #184: a unit named under `when` exists only while one of its values is
// set. An unset value leaves no unit behind, and the dropped unit's own
// placeholders need no value.
func TestWhenDropsAUnitWhoseValuesAreUnset(t *testing.T) {
	repo := box46(t, "")
	chatSvc(t, repo, "SIGNAL_ACCOUNT: \"\"\nMATRIX_HOMESERVER: https://hs.example.org\nADMIN_NUMBER: \"\"\nADMIN_MXID: \"@admin:example.org\"\n")
	st, err := Load(repo)
	if err != nil {
		t.Fatal(err) // ${SIGNAL_IMAGE} has no value, but its unit is off
	}
	units := st.Services["chat"].UnitNames()
	if strings.Join(units, ",") != "chat-relay.service,chat.service" {
		t.Fatalf("units: %v (chat-signal is off, chat-relay is on through ADMIN_MXID)", units)
	}
	chatSvc(t, repo, "SIGNAL_ACCOUNT: \"+4917012345678\"\nSIGNAL_IMAGE: sig\nADMIN_NUMBER: \"\"\nADMIN_MXID: \"\"\n")
	st, err = Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if units := st.Services["chat"].UnitNames(); strings.Join(units, ",") != "chat-signal.service,chat.service" {
		t.Fatalf("units: %v (no approval identity: no relay)", units)
	}
	// a `when` that names no unit of the service is a typo, not a silent no-op
	writeSvc(t, repo, "typo", "name: typo\ntemplate: {registry: builtin, name: typo, version: 1}\nhealth: {none: test}\nwhen: {typo-sigal.container: [IMAGE]}\n",
		map[string]string{"typo.container": "[Container]\nImage=${IMAGE}\n"})
	if _, err := Load(repo); err == nil || !strings.Contains(err.Error(), "typo-sigal.container") {
		t.Fatalf("an unknown file under when must be refused: %v", err)
	}
}

// #184, ADR 0022 §3: a person's target on a channel no service delivers on
// fails at plan time, naming the value that would switch it on.
func TestPeopleOnlyOnConfiguredChannels(t *testing.T) {
	signalOnly := "SIGNAL_ACCOUNT: \"+4917012345678\"\nSIGNAL_IMAGE: sig\nMATRIX_HOMESERVER: \"\"\nTELEGRAM_ALLOWED_USERS: \"\"\nADMIN_NUMBER: \"+4917012345679\"\nADMIN_MXID: \"\"\n"
	repo := box46(t, "people: {mama: [\"signal:+4917012345678\"]}\n")
	chatSvc(t, repo, signalOnly)
	if _, err := Load(repo); err != nil {
		t.Fatalf("a signal target on a signal box: %v", err)
	}
	repo = box46(t, "people: {lena: [\"signal:+4917012345678\", \"matrix:!raum:example.org\"]}\n")
	chatSvc(t, repo, signalOnly)
	if _, err := Load(repo); err == nil || !strings.Contains(err.Error(), "people.lena") || !strings.Contains(err.Error(), "MATRIX_HOMESERVER") {
		t.Fatalf("a matrix target on a box without matrix must fail naming the value: %v", err)
	}
	repo = box46(t, "people: {papa: [\"discord:123\"]}\n")
	chatSvc(t, repo, signalOnly)
	if _, err := Load(repo); err == nil || !strings.Contains(err.Error(), "discord") {
		t.Fatalf("a platform no service delivers on must fail: %v", err)
	}
}

// #184: a service that declares channels has at least one set, and every
// channel is a platform people are reached on.
func TestAChatServiceHasAChannel(t *testing.T) {
	repo := box46(t, "")
	chatSvc(t, repo, "SIGNAL_ACCOUNT: \"\"\nMATRIX_HOMESERVER: \"\"\nTELEGRAM_ALLOWED_USERS: \"\"\nADMIN_NUMBER: \"\"\nADMIN_MXID: \"\"\n")
	if _, err := Load(repo); err == nil || !strings.Contains(err.Error(), "no channel") || !strings.Contains(err.Error(), "SIGNAL_ACCOUNT") {
		t.Fatalf("a chat service without any channel must be refused: %v", err)
	}
	repo = box46(t, "")
	writeSvc(t, repo, "brief", "name: brief\ntemplate: {registry: builtin, name: brief, version: 1}\nhealth: {none: test}\nchannels: {brieftaube: IMAGE}\n",
		map[string]string{"brief.container": "[Container]\nImage=${IMAGE}\n"})
	if _, err := Load(repo); err == nil || !strings.Contains(err.Error(), "brieftaube") {
		t.Fatalf("an unknown platform under channels must be refused: %v", err)
	}
}
