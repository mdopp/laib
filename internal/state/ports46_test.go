package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSvc(t *testing.T, repo, name, contract string, units map[string]string) {
	t.Helper()
	dir := filepath.Join(repo, "services", name)
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "laib.yml"), []byte(contract), 0o644)
	os.WriteFile(filepath.Join(dir, "values.yml"), []byte("IMAGE: img\n"), 0o644)
	for f, body := range units {
		os.WriteFile(filepath.Join(dir, f), []byte(body), 0o644)
	}
}

func box46(t *testing.T, laib string) string {
	t.Helper()
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "box.yml"), []byte("domain: x.home\ndataDir: /tmp/d\nroles: {}\nhostNetwork: []\n"+laib), 0o644)
	os.WriteFile(filepath.Join(repo, "policy.yml"), []byte("tiers: [read, propose, apply, destroy]\nhands: {human: {ceiling: destroy}}\n"), 0o644)
	return repo
}

// #46: two services publishing the same host port never both start; the
// state refuses it, naming both.
func TestPublishedPortsMustBeUniqueAcrossServices(t *testing.T) {
	repo := box46(t, "")
	c := "name: %s\ntemplate: {registry: builtin, name: %s, version: 1}\nroutes: []\nsecrets: []\ndata: []\nhealth: {http: http://127.0.0.1:1/}\n"
	writeSvc(t, repo, "a", strings.ReplaceAll(c, "%s", "a"), map[string]string{"a.container": "[Container]\nImage=${IMAGE}\nPublishPort=127.0.0.1:8080:80\n"})
	writeSvc(t, repo, "b", strings.ReplaceAll(c, "%s", "b"), map[string]string{"b.container": "[Container]\nImage=${IMAGE}\nPublishPort=8080:8080\n"})
	_, err := Load(repo)
	if err == nil || !strings.Contains(err.Error(), "8080") || !strings.Contains(err.Error(), "a") || !strings.Contains(err.Error(), "b") {
		t.Fatalf("the port clash must be refused naming both services: %v", err)
	}
}

// #46 / ADR 0012 §4: a template that talks to laib's MCP from a container
// declares needs: [mcp]; with laib.listen on loopback the state refuses
// it in plain words instead of a service that silently cannot reach laib.
func TestATemplateNeedingMCPRefusesALoopbackListener(t *testing.T) {
	c := "name: bot\ntemplate: {registry: builtin, name: bot, version: 1}\nneeds: [mcp]\nroutes: []\nsecrets: []\ndata: []\nhealth: {http: http://127.0.0.1:1/}\n"
	repo := box46(t, "")
	writeSvc(t, repo, "bot", c, map[string]string{"bot.container": "[Container]\nImage=${IMAGE}\n"})
	if _, err := Load(repo); err == nil || !strings.Contains(err.Error(), "laib.listen") {
		t.Fatalf("needs: [mcp] with a loopback listener must be refused: %v", err)
	}
	repo = box46(t, "laib: {listen: 0.0.0.0}\n")
	writeSvc(t, repo, "bot", c, map[string]string{"bot.container": "[Container]\nImage=${IMAGE}\n"})
	if _, err := Load(repo); err != nil {
		t.Fatalf("with listen 0.0.0.0 the service is fine: %v", err)
	}
}
