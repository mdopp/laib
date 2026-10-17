package render

import (
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// #233: only an Environment= line may carry a secret-backed value; there it
// becomes a podman secret (#224). Anywhere else in a unit (Exec=,
// PodmanArgs=, Label=, a host unit's ExecStart=, a comment) the value would
// land in the unit file and on argv, so render refuses it: at lint, where no
// secret is given, and at plan and apply, where they are. The error names
// the template line, never the value.
func TestASecretOutsideEnvironmentIsRefused(t *testing.T) {
	values := map[string]state.Value{"PW": {Secret: "PW"}, "U": {Literal: "x"}}
	for _, unit := range []struct{ file, line string }{
		{"a.container", "Exec=serve --token ${PW}"},
		{"a.container", "PodmanArgs=--env TOKEN=${PW}"},
		{"a.container", "Label=token=${PW}"},
		{"a.container", "# the token: ${PW}"},
		{"a.container", "Environment=A=1\nExec=serve ${U} ${PW}"},
		{"a.container", "Exec=serve ${MODEL_KEY}"}, // the builtin is a secret too
		{"a.pod", "PodmanArgs=--label t=${PW}"},
		{"a.service", "ExecStart=/usr/bin/serve --token ${PW}"},
	} {
		head := "[Container]\nImage=x\n"
		if !strings.HasSuffix(unit.file, ".container") {
			head = "[Service]\n"
		}
		st := shareBox(map[string]string{unit.file: head + unit.line + "\n"}, values)
		st.Box.Model = state.Model{URL: "http://m/v1", KeySecret: "PW"}
		for _, secrets := range []map[string]string{nil, {"PW": "pw-v233"}} {
			_, err := Render(st, secrets)
			if err == nil {
				t.Errorf("%s: %q rendered with the secret off Environment= (secrets given: %v)", unit.file, unit.line, secrets != nil)
				continue
			}
			bad := unit.line[strings.LastIndex(unit.line, "\n")+1:]
			if msg := err.Error(); strings.Contains(msg, "pw-v233") || !strings.Contains(msg, "services/share/"+unit.file) || !strings.Contains(msg, bad) {
				t.Errorf("%s: the error must name the template line %q and never the value: %v", unit.file, bad, err)
			}
		}
	}
	// an Environment= line still carries one, in a .container and a host unit
	for file, content := range map[string]string{
		"a.container": "[Container]\nImage=x\nEnvironment=TOKEN=${PW}\nExec=serve ${U}\n",
		"a.service":   "[Service]\nEnvironment=TOKEN=${PW}\nExecStart=/usr/bin/serve\n",
	} {
		if _, err := Render(shareBox(map[string]string{file: content}, values), map[string]string{"PW": "pw-v233"}); err != nil {
			t.Errorf("%s: a secret on an Environment= line is the sanctioned way: %v", file, err)
		}
	}
}
