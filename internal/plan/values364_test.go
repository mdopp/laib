package plan

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/state"
)

// #364: a value only a hook reads (auth's SMTP_*, OPERATOR_EMAIL, BASE_DN)
// changes no unit, so plan saw nothing and pre-start never ran again. The
// service's values are an output of their own: changing one is a change of
// the service, whoever reads it. The first time the file is written (a box
// from before #364) it is no change of the service: nothing restarts for it.
func TestAChangedValueIsAChangeOfItsService(t *testing.T) {
	st, err := state.Load("../../testdata/golden/basic/state")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	paths := boxfs.Paths{Quadlet: filepath.Join(home, "q"), Render: filepath.Join(home, "r"), Systemd: filepath.Join(home, "s")}
	var secrets map[string]string // no podman secrets to compare here
	write := func(p *Plan, skip string) {
		for k, v := range p.Output {
			if k == skip {
				continue
			}
			target := Target(paths, k)
			os.MkdirAll(filepath.Dir(target), 0o755)
			os.WriteFile(target, []byte(v), 0o644)
		}
	}
	p, err := Compute(context.Background(), st, secrets, paths, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Output["quadlet/auth/values.laib"]; !ok {
		t.Fatalf("no values output for auth: %v", p.Output)
	}
	// a box from before #364: everything applied, the values file not yet there
	write(p, "quadlet/auth/values.laib")
	p, _ = Compute(context.Background(), st, secrets, paths, "")
	if len(p.Changes) != 1 || p.Changes[0].Path != "quadlet/auth/values.laib" || len(p.Services) != 0 {
		t.Fatalf("the first values file is written, and no service changes for it: %+v %v", p.Changes, p.Services)
	}
	write(p, "")
	st.Services["auth"].Values["SMTP_HOST"] = state.Value{Literal: "smtp.example.home"}
	p, _ = Compute(context.Background(), st, secrets, paths, "")
	// SMTP_HOST also turns on the owner's mail: laib-notify's container, its token and timer (#443, ADR 0027)
	var own []Change
	for _, c := range p.Changes {
		if !strings.Contains(c.Path, "laib-notify") {
			own = append(own, c)
		}
	}
	if !slices.Equal(p.Services, []string{"auth", "laib-notify"}) || len(own) != 1 || own[0].Kind != "change" || len(p.Changes) != 3 {
		t.Fatalf("a hook-only value must change its service: %+v %v", p.Changes, p.Services)
	}
}
