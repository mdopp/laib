package templates

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// #166: an external secret the service works without must say optional:
// true, or `laib add` refuses until someone sets it (cmd/laib applyHeld).
func TestSecretsTheServiceWorksWithoutAreOptional(t *testing.T) {
	for svc, names := range map[string][]string{
		"bote": {"BOTE_MODEL_KEY", "BOTE_HA_TOKEN"},                                 // a cloud model only; without HA the ha tool is absent (README)
		"haus": {"HAUS_HA_TOKEN", "HAUS_RADICALE_PASSWORD", "HAUS_PAPERLESS_TOKEN"}, // without it the house paths stay on the Solaris bridge (haus#10)
	} {
		b, err := os.ReadFile(filepath.Join(svc, "laib.yml"))
		if err != nil {
			t.Fatal(err)
		}
		var c struct {
			Secrets []struct {
				Name     string `yaml:"name"`
				External bool   `yaml:"external"`
				Optional bool   `yaml:"optional"`
			} `yaml:"secrets"`
		}
		if err := yaml.Unmarshal(b, &c); err != nil {
			t.Fatal(err)
		}
		for _, want := range names {
			found := false
			for _, s := range c.Secrets {
				if s.Name == want {
					found = true
					if !s.External || !s.Optional {
						t.Errorf("%s: %s must be external and optional: laib add %s refuses without it", svc, want, svc)
					}
				}
			}
			if !found {
				t.Errorf("%s: %s is not declared", svc, want)
			}
		}
	}
}
