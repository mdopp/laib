package render

import (
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// #797 (ADR 0026 §4): laib wish is served by laib-haushalt, so the GitHub
// token is its secret: declared with box.yml intake, an optional key the
// owner sets, handed to its container as a podman secret. The kernel only
// keeps it in secrets.age like every service's secret; without an intake
// the unit carries no token.
func TestTheIntakeTokenIsLaibHaushaltsSecret(t *testing.T) {
	st, err := state.Load("../../testdata/golden/nuc/state")
	if err != nil {
		t.Fatal(err)
	}
	st.Box.Intake.Repo = "mdopp/haus-wuensche"
	sec := map[string]string{HaushaltToken: "hh-token-1", HaushaltKey: "hh-key-2", IntakeToken: "ghp_intake"}
	out, err := Render(st, sec)
	if err != nil {
		t.Fatal(err)
	}
	unit := out["quadlet/laib-haushalt/laib-haushalt.container"]
	if !strings.Contains(unit, "\nSecret=laib.laib-haushalt.laib-haushalt.GITHUB_INTAKE_TOKEN,type=env,target=GITHUB_INTAKE_TOKEN\n") || strings.Contains(unit, "ghp_intake") {
		t.Errorf("laib-haushalt does not get the intake token as a podman secret:\n%s", unit)
	}
	if PodmanSecrets(st, sec)["laib.laib-haushalt.laib-haushalt.GITHUB_INTAKE_TOKEN"] != "ghp_intake" {
		t.Error("the intake token is no podman secret")
	}
	if !slices.ContainsFunc(SecretNames(st), func(s state.Secret) bool { return s.Name == IntakeToken && s.External && s.Optional }) {
		t.Errorf("the intake token is not declared with box.yml intake: %+v", SecretNames(st))
	}

	st.Box.Intake.Repo = ""
	out, _ = Render(st, sec)
	if strings.Contains(out["quadlet/laib-haushalt/laib-haushalt.container"], "GITHUB_INTAKE_TOKEN") || slices.ContainsFunc(SecretNames(st), func(s state.Secret) bool { return s.Name == IntakeToken }) {
		t.Error("without box.yml intake laib-haushalt gets no GitHub token")
	}
}
