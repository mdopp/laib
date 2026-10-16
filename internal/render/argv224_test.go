package render

import (
	"regexp"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// asContainerSees turns each Secret=…,type=env line back into the
// Environment= line the container's environment ends up with (#224), for
// the tests that read what a container gets.
func asContainerSees(st *state.State, secrets map[string]string, out Output) Output {
	sec := PodmanSecrets(st, secrets)
	re := regexp.MustCompile(`(?m)^Secret=([^,\n]+),type=env,target=(\S+)$`)
	for p, c := range out {
		out[p] = re.ReplaceAllStringFunc(c, func(l string) string {
			m := re.FindStringSubmatch(l)
			return "Environment=" + m[2] + "=" + sec[m[1]]
		})
	}
	return out
}

// #224: Quadlet turns every line of a .container into `podman run` argv
// (Environment=K=V becomes --env K=V), and ps shows that argv to every user
// on the box. Rendered with every declared secret set, no Quadlet file of a
// shipped template carries a secret's value: not in Environment=, not
// anywhere.
func TestNoSecretValueInAQuadletUnit(t *testing.T) {
	st := shippedBox(t)
	secrets := map[string]string{}
	for _, s := range SecretNames(st) {
		secrets[s.Name] = "fake-" + s.Name + "-v224"
	}
	out, err := Render(st, secrets)
	if err != nil {
		t.Fatal(err)
	}
	units := 0
	for p, c := range out {
		if !strings.HasPrefix(p, "quadlet/") {
			continue
		}
		units++
		for _, line := range strings.Split(c, "\n") {
			for name, v := range secrets {
				if strings.Contains(line, v) {
					t.Errorf("%s: the value of %s is on podman run's argv: %s", p, name, line)
				}
			}
		}
	}
	if units < 30 || out["quadlet/werkstatt/werkstatt.container"] == "" || out["quadlet/werkstatt/werkstatt-autoloop.container"] == "" {
		t.Fatalf("only %d Quadlet files rendered, or the werkstatt units are missing; the walk is broken", units)
	}
}
