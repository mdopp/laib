package render

import (
	"strings"
	"testing"
)

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
