package templates

import (
	"regexp"
	"strings"
	"testing"
)

// #551 (9): bote-signal was unhealthy for good. It runs signal-cli's own
// daemon in the signal-cli-rest-api image (#469), and inherits the image's
// HEALTHCHECK, which asks the REST API's /v1/health: a path the daemon
// does not serve. The check asks the daemon where Exec makes it listen, on
// the path Hermes and the relay depend on, and fails on an HTTP error
// (curl without -f exits 0 on a 404: a probe that cannot fail).
func TestBoteSignalHealthAsksTheDaemonItRuns(t *testing.T) {
	unit := readT(t, "bote/bote-signal.container")
	m := regexp.MustCompile(`(?m)^Exec=.* daemon --http (\S+)$`).FindStringSubmatch(unit)
	if m == nil {
		t.Fatalf("no signal-cli daemon --http in Exec:\n%s", unit)
	}
	hc := regexp.MustCompile(`(?m)^HealthCmd=(.*)$`).FindStringSubmatch(unit)
	if hc == nil {
		t.Fatalf("no HealthCmd: the image's own check (/v1/health of the REST API) asks a path the daemon does not serve\n%s", unit)
	}
	cmd := hc[1]
	if !strings.Contains(cmd, "http://"+m[1]+"/api/v1/check") {
		t.Errorf("HealthCmd %q does not ask the daemon's /api/v1/check on %s", cmd, m[1])
	}
	if !regexp.MustCompile(`\bcurl\b.* -[a-zA-Z]*f`).MatchString(cmd) {
		t.Errorf("HealthCmd %q does not fail on an HTTP error (curl -f)", cmd)
	}
}
