package templates

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// #1130 (ADR 0034): an image named by a version tag (reitti:5.9.1,
// immich-public-proxy:4.1.2) is pinned by digest in the
// template, so a newly added service lands pinned. A floating default
// (:latest, :stable, a bare major) is still pinned on the box by „Images festhalten“;
// this test does not let a version tag float again.
func TestAVersionTaggedImageIsPinnedByDigest(t *testing.T) {
	version := regexp.MustCompile(`:v?\d+\.\d+(\.\d+)?$`) // registry.semverTag: what the pin card missed until #1130
	files, _ := filepath.Glob("*/values.example.yml")
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var vals map[string]any
		if err := yaml.Unmarshal(b, &vals); err != nil {
			t.Fatal(err)
		}
		for k, v := range vals {
			s, _ := v.(string)
			if strings.HasSuffix(k, "IMAGE") && version.MatchString(s) && !strings.Contains(s, "@sha256:") {
				t.Errorf("%s: %s=%s names a version but no digest: pin it as repo:tag@sha256:… (ADR 0034)", f, k, s)
			}
		}
	}
}
