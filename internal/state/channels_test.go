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

// #184 review: the rule for "set". Absent from values.yml is an error under
// when; empty, whitespace and YAML null (~, null) are unset and render as
// empty; `false` is text and counts as set; a !secret never counts, because
// plan does not know secrets. plan names why a unit is off.
func TestWhenRules(t *testing.T) {
	c := "name: w\ntemplate: {registry: builtin, name: w, version: 1}\nhealth: {none: test}\nsecrets: [{name: S, external: true, optional: true}]\n" +
		"when: {a.container: [A], b.container: [B], c.container: [C], d.container: [D], e.container: [E], f.container: [F]}\n"
	units := map[string]string{"w.container": "[Container]\nImage=${IMAGE}\nEnvironment=B=${B}\n"}
	for _, u := range []string{"a", "b", "c", "d", "e", "f"} {
		units[u+".container"] = "[Container]\nImage=${IMAGE}\n"
	}
	repo := box46(t, "")
	writeSvc(t, repo, "w", c, units)
	os.WriteFile(filepath.Join(repo, "services", "w", "values.yml"), []byte("IMAGE: img\nA: \"\"\nB: ~\nC: null\nD: \"   \"\nE: false\nF: !secret S\n"), 0o644)
	st, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(st.Services["w"].UnitNames(), ","); got != "e.service,w.service" {
		t.Fatalf("units: %s (only E=false is set)", got)
	}
	if v := st.Services["w"].Values["B"].Literal; v != "" {
		t.Fatalf("a YAML null renders empty, not %q", v)
	}
	off := strings.Join(st.Off, "\n")
	for _, want := range []string{"services/w/a.container", "A", "services/w/f.container", "F"} {
		if !strings.Contains(off, want) {
			t.Fatalf("plan must say why a unit is off (%s): %q", want, off)
		}
	}
	// a value under when that values.yml does not have is a typo
	writeSvc(t, repo, "x", "name: x\ntemplate: {registry: builtin, name: x, version: 1}\nhealth: {none: test}\nwhen: {x2.container: [SIGNAL_ACOUNT]}\n",
		map[string]string{"x.container": "[Container]\nImage=${IMAGE}\n", "x2.container": "[Container]\nImage=${IMAGE}\n"})
	if _, err := Load(repo); err == nil || !strings.Contains(err.Error(), "SIGNAL_ACOUNT") {
		t.Fatalf("an unknown value under when must be refused: %v", err)
	}
}

// #184 review: a unit that stays may not lean on one that went: dropping a
// pod a container joins, or a unit another Requires= or BindsTo=, is an error.
func TestWhenKeepsWhatRemainingUnitsNeed(t *testing.T) {
	for name, unit := range map[string]string{
		"Pod":      "[Container]\nImage=${IMAGE}\nPod=p.pod\n",
		"Requires": "[Unit]\nRequires=p-side.service\n[Container]\nImage=${IMAGE}\n",
		"BindsTo":  "[Unit]\nBindsTo=p-side.service\n[Container]\nImage=${IMAGE}\n",
	} {
		repo := box46(t, "")
		writeSvc(t, repo, "p", "name: p\ntemplate: {registry: builtin, name: p, version: 1}\nhealth: {none: test}\nwhen: {p.pod: [OFF], p-side.container: [OFF]}\n",
			map[string]string{"p.container": unit, "p.pod": "[Pod]\nPodName=p\n", "p-side.container": "[Container]\nImage=${IMAGE}\n"})
		os.WriteFile(filepath.Join(repo, "services", "p", "values.yml"), []byte("IMAGE: img\nOFF: \"\"\n"), 0o644)
		if _, err := Load(repo); err == nil || !strings.Contains(err.Error(), "p.container") {
			t.Errorf("%s: a remaining unit needs a dropped one: %v", name, err)
		}
	}
}

// #184 review: `requires` says a set value needs others set. Literal ones
// fail at load; a secret one is for plan, which knows secrets. Both names
// must be values of the service. A !secret channel value is never on.
func TestRequiresBetweenValues(t *testing.T) {
	c := "name: r\ntemplate: {registry: builtin, name: r, version: 1}\nhealth: {none: test}\nsecrets: [{name: TOK, external: true, optional: true}]\n" +
		"requires: {ADMIN: [ACCOUNT], ACCOUNT: [TOKEN]}\nchannels: {signal: ACCOUNT, telegram: TOKEN}\n"
	load := func(values string) (*State, error) {
		repo := box46(t, "")
		writeSvc(t, repo, "r", c, map[string]string{"r.container": "[Container]\nImage=${IMAGE}\n"})
		os.WriteFile(filepath.Join(repo, "services", "r", "values.yml"), []byte("IMAGE: img\nTOKEN: !secret TOK\n"+values), 0o644)
		return Load(repo)
	}
	if _, err := load("ADMIN: \"+4917012345679\"\nACCOUNT: \"\"\n"); err == nil || !strings.Contains(err.Error(), "ADMIN") || !strings.Contains(err.Error(), "ACCOUNT") {
		t.Fatalf("ADMIN without ACCOUNT must fail at load, naming both: %v", err)
	}
	st, err := load("ADMIN: \"+4917012345679\"\nACCOUNT: \"+4917012345678\"\n")
	if err != nil {
		t.Fatalf("a secret requirement waits for plan: %v", err)
	}
	if got := st.Services["r"].Unmet(map[string]string{}); len(got) != 1 || !strings.Contains(got[0], "TOK") {
		t.Fatalf("plan without the secret: %v", got)
	}
	if got := st.Services["r"].Unmet(map[string]string{"TOK": "x"}); len(got) != 0 {
		t.Fatalf("plan with the secret: %v", got)
	}
	if _, err := load("ADMIN: \"\"\nACCOUNT: \"\"\n"); err == nil || !strings.Contains(err.Error(), "no channel") {
		t.Fatalf("a !secret channel value is never on: %v", err)
	}
	repo := box46(t, "")
	writeSvc(t, repo, "q", "name: q\ntemplate: {registry: builtin, name: q, version: 1}\nhealth: {none: test}\nrequires: {IMAGE: [NOPE]}\n",
		map[string]string{"q.container": "[Container]\nImage=${IMAGE}\n"})
	if _, err := Load(repo); err == nil || !strings.Contains(err.Error(), "NOPE") {
		t.Fatalf("an unknown value under requires must be refused: %v", err)
	}
}

// #190: a port's `when` that names no value of the service is a typo, not
// a port that is silently always off.
func TestPortWhenNamesAValue(t *testing.T) {
	repo := box46(t, "")
	writeSvc(t, repo, "p", "name: p\ntemplate: {registry: builtin, name: p, version: 1}\nhealth: {none: test}\nports: [{port: 8085, when: [SIGNAL_ACOUNT]}]\n",
		map[string]string{"p.container": "[Container]\nImage=${IMAGE}\nPublishPort=127.0.0.1:8085:8080\n"})
	os.WriteFile(filepath.Join(repo, "services", "p", "values.yml"), []byte("IMAGE: img\n"), 0o644)
	if _, err := Load(repo); err == nil || !strings.Contains(err.Error(), "SIGNAL_ACOUNT") {
		t.Fatalf("an unknown value under a port's when must be refused: %v", err)
	}
}
