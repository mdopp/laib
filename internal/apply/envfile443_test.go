package apply

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/state"
)

// #443 (#449 review): a host unit Laib renders carries no secret value; its
// secrets sit in <unit>.env (EnvironmentFile=), which apply writes 0600.
// laib-notify's token and SMTP password, and the Operator's token, model key
// and usage-metrics token.
func TestHostUnitsCarryNoSecretAndTheirEnvFileIsPrivate(t *testing.T) {
	st, err := state.Load("../../testdata/golden/nuc/state")
	if err != nil {
		t.Fatal(err)
	}
	st.Box.Operator.Enabled = true
	st.Box.Watchdog.URL = "https://hc.example/ping/abc"
	sec := map[string]string{render.OperatorToken: "op-secret-1", render.NotifyToken: "notify-secret-2", "USAGE_METRICS_TOKEN": "usage-secret-3", "AUTHELIA_SMTP_PASSWORD": "smtp-secret-4"}
	if st.Box.Model.KeySecret != "" {
		sec[st.Box.Model.KeySecret] = "model-secret-5"
	}
	out, err := render.Render(st, sec)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"laib-operator", "laib-notify"} {
		unit, env := out["systemd/"+u+".service"], out["systemd/"+u+".env"]
		if unit == "" || env == "" {
			t.Fatalf("%s: unit or env file not rendered", u)
		}
		for _, v := range sec {
			if strings.Contains(unit, v) {
				t.Errorf("%s.service carries a secret value", u)
			}
		}
		if !strings.Contains(unit, "EnvironmentFile=%h/.config/systemd/user/"+u+".env") {
			t.Errorf("%s.service does not read its env file:\n%s", u, unit)
		}
	}
	if !strings.Contains(out["systemd/laib-operator.env"], `LAIB_TOKEN="op-secret-1"`) || !strings.Contains(out["systemd/laib-notify.env"], `LAIB_TOKEN="notify-secret-2"`) {
		t.Errorf("the env files lack the tokens:\n%s\n%s", out["systemd/laib-operator.env"], out["systemd/laib-notify.env"])
	}
	target := filepath.Join(t.TempDir(), "laib-notify.env")
	if err := writeAtomic(target, out["systemd/laib-notify.env"]); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o600 {
		t.Errorf("the env file is %v, want 0600", fi.Mode().Perm())
	}
}
