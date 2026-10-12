package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTemplate46: a minimal template folder; extra files on top.
func writeTemplate46(t *testing.T, root, name, contract string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "laib.yml"), []byte(contract), 0o644)
	for f, body := range files {
		mode := os.FileMode(0o644)
		if f == "pre-start" || f == "post-deploy" {
			mode = 0o755
		}
		os.WriteFile(filepath.Join(dir, f), []byte(body), mode)
	}
	return dir
}

// #46: a template without a health check passes lint today; a service that
// cannot be probed must say so (health: {none: "<why>"}).
func TestLintRequiresAHealthCheckOrAReason(t *testing.T) {
	root := t.TempDir()
	c := "name: t\ntemplate: {registry: builtin, name: t, version: 1}\nroutes: []\nsecrets: []\ndata: []\n"
	dir := writeTemplate46(t, root, "t", c, map[string]string{"t.container": "[Container]\nImage=img\n"})
	if lintTemplate(dir) == exitOK {
		t.Fatal("a template without any health check passed lint")
	}
	writeTemplate46(t, root, "t", c+"health: {none: \"MQTT speaks no HTTP; the unit state is the check\"}\n", nil)
	if lintTemplate(dir) != exitOK {
		t.Fatal("an explicit reason is a valid health declaration")
	}
	writeTemplate46(t, root, "t", c+"health: {http: http://127.0.0.1:80/}\n", nil)
	if lintTemplate(dir) != exitOK {
		t.Fatal("an http probe is a valid health declaration")
	}
}

// #46: whoami and the bote pod both published 127.0.0.1:8080; lint sees
// the sibling templates and refuses the clash.
func TestLintFindsAPortClashWithASiblingTemplate(t *testing.T) {
	root := t.TempDir()
	writeTemplate46(t, root, "a", "name: a\ntemplate: {registry: builtin, name: a, version: 1}\nroutes: []\nsecrets: []\ndata: []\nhealth: {http: http://127.0.0.1:8080/}\n", map[string]string{"a.container": "[Container]\nImage=img\nPublishPort=127.0.0.1:8080:80\n"})
	dir := writeTemplate46(t, root, "b", "name: b\ntemplate: {registry: builtin, name: b, version: 1}\nroutes: []\nsecrets: []\ndata: []\nhealth: {http: http://127.0.0.1:8080/}\n", map[string]string{"b.pod": "[Pod]\nPublishPort=127.0.0.1:8080:8080\n", "b-x.container": "[Container]\nImage=img\nPod=b.pod\n"})
	if lintTemplate(dir) == exitOK {
		t.Fatal("two templates publishing the same host port passed lint")
	}
	writeTemplate46(t, root, "b", "name: b\ntemplate: {registry: builtin, name: b, version: 1}\nroutes: []\nsecrets: []\ndata: []\nhealth: {http: http://127.0.0.1:8085/}\n", map[string]string{"b.pod": "[Pod]\nPublishPort=127.0.0.1:8085:8080\n"})
	if lintTemplate(dir) != exitOK {
		t.Fatal("distinct ports are fine")
	}
}
