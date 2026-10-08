package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func minimal(t *testing.T) string {
	dir := t.TempDir()
	write(t, dir, "box.yml", "domain: x.home\ndataDir: /d\nroles: {sso: auth}\n")
	write(t, dir, "policy.yml", "tiers: [read, apply]\nhands: {human: {ceiling: apply}}\n")
	write(t, dir, "services/auth/laib.yml", "name: auth\nroutes: [{host: auth, port: 9091, exposure: public, auth: none}]\n")
	write(t, dir, "services/auth/auth.container", "[Container]\nImage=x\n")
	return dir
}

func TestLoadMinimal(t *testing.T) {
	st, err := Load(minimal(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Services["auth"].UnitNames(); len(got) != 1 || got[0] != "auth.service" {
		t.Fatalf("units %v", got)
	}
}

func TestLoadReportsEveryProblemAtOnce(t *testing.T) {
	dir := minimal(t)
	write(t, dir, "services/bad/laib.yml", "name: wrong\nroutes: [{host: auth, port: 0, exposure: lan, auth: maybe}]\ngpu: true\n")
	write(t, dir, "services/bad/bad.container", "Image=${IMAGE}\nEnvironment=K=${PW}\n")
	write(t, dir, "services/bad/values.yml", "IMAGE: x\nPW: !secret NOPE\n")
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{
		`name is "wrong"`, "exposure must be", "auth must be", "needs host and port",
		`host "auth" already published by auth`, "undeclared secret NOPE", "gpu.present is false",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%s", want, err)
		}
	}
}

func TestSecretValuesAreTagged(t *testing.T) {
	dir := minimal(t)
	write(t, dir, "services/auth/laib.yml", "name: auth\nsecrets: [{name: S}]\nroutes: [{host: auth, port: 9091, exposure: public, auth: none}]\n")
	write(t, dir, "services/auth/values.yml", "A: literal\nB: !secret S\n")
	st, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	v := st.Services["auth"].Values
	if v["A"].Literal != "literal" || v["B"].Secret != "S" || v["B"].Literal != "" {
		t.Fatalf("%+v", v)
	}
}

func TestPlaceholdersAreDistinctAndOrdered(t *testing.T) {
	got := Placeholders("${B} ${A} ${B} $A ${lower}")
	if strings.Join(got, ",") != "B,A" {
		t.Fatal(got)
	}
}
