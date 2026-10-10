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

func TestNetworkContractIsLinted(t *testing.T) {
	dir := minimal(t)
	write(t, dir, "services/auth/auth.container", "[Container]\nImage=x\nNetwork=host\n")
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "does not declare hostNetwork") {
		t.Fatalf("unit on host network without contract accepted: %v", err)
	}
	write(t, dir, "services/auth/laib.yml", "name: auth\nhostNetwork: true\nroutes: [{host: auth, port: 9091, exposure: public, auth: none}]\n")
	_, err = Load(dir)
	if err == nil || !strings.Contains(err.Error(), "closed carve-out list") {
		t.Fatalf("hostNetwork outside the carve-out list accepted: %v", err)
	}
	write(t, dir, "box.yml", "domain: x.home\ndataDir: /d\nroles: {sso: auth}\nhostNetwork: [auth]\n")
	if _, err := Load(dir); err != nil {
		t.Fatal(err)
	}
}

func TestOrderRolesFirstThenAfter(t *testing.T) {
	dir := minimal(t)
	write(t, dir, "services/zed/laib.yml", "name: zed\nafter: [bee]\n")
	write(t, dir, "services/zed/zed.container", "[Container]\nImage=x\n")
	write(t, dir, "services/bee/laib.yml", "name: bee\n")
	write(t, dir, "services/bee/bee.container", "[Container]\nImage=x\n")
	write(t, dir, "services/aaa/laib.yml", "name: aaa\nafter: [zed]\n")
	write(t, dir, "services/aaa/aaa.container", "[Container]\nImage=x\n")
	st, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(st.Order(st.ServiceNames()), ",")
	if got != "auth,bee,zed,aaa" {
		t.Fatal(got)
	}
	write(t, dir, "services/aaa/laib.yml", "name: aaa\nafter: [ghost, aaa]\n")
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "ghost") || !strings.Contains(err.Error(), "names itself") {
		t.Fatalf("bad after accepted: %v", err)
	}
}

func TestTwoServicesForOnePurposeWarn(t *testing.T) {
	dir := minimal(t)
	write(t, dir, "services/auth/laib.yml", "name: auth\nprovides: [sso]\nroutes: [{host: auth, port: 9091, exposure: public, auth: none}]\n")
	write(t, dir, "services/keycloak/laib.yml", "name: keycloak\nprovides: [sso, oidc]\n")
	write(t, dir, "services/keycloak/keycloak.container", "[Container]\nImage=x\n")
	st, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Warnings) != 1 || !strings.Contains(st.Warnings[0], `"sso"`) || !strings.Contains(st.Warnings[0], "auth, keycloak") {
		t.Fatalf("%v", st.Warnings)
	}
}

func TestModelIsLocalRoleOrRemoteURL(t *testing.T) {
	dir := minimal(t)
	write(t, dir, "box.yml", "domain: x.home\ndataDir: /d\nroles: {sso: auth}\nmodel: {url: http://10.0.0.20:11435/v1, keySecret: MODEL_API_KEY}\n")
	st, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.ModelURL() != "http://10.0.0.20:11435/v1" || st.ModelLease() != "" {
		t.Fatal(st.ModelURL(), st.ModelLease())
	}
	write(t, dir, "box.yml", "domain: x.home\ndataDir: /d\ngpu: {present: true}\nroles: {sso: auth, model: auth}\nmodel: {url: http://x/v1}\n")
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "both set") {
		t.Fatalf("role and url together accepted: %v", err)
	}
	write(t, dir, "box.yml", "domain: x.home\ndataDir: /d\ngpu: {present: true}\nroles: {sso: auth, model: auth}\n")
	st, _ = Load(dir)
	if st.ModelURL() != "http://127.0.0.1:9091/v1" {
		t.Fatal(st.ModelURL())
	}
}
