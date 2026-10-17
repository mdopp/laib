package main

import (
	"path/filepath"
	"testing"

	"github.com/mdopp/laib/internal/cli"
)

// #230: the shared file-share tree is never relabelled. :z/:Z on it makes
// podman relabel the whole multi-writer tree on every start, and the
// rootless lsetxattr fails on any entry core does not own (exit 125, a
// crash loop; ServiceBay #1731, #1923). A unit that mounts it turns
// labelling off instead.
func TestLintRefusesARelabelledShare(t *testing.T) {
	root := t.TempDir()
	writeTemplate(t, root, "file-share", "name: file-share\ntemplate: {registry: builtin, name: file-share, version: 1}\nroutes: []\nsecrets: []\ndata:\n  - path: ${DATA_DIR}/file-share/data\nhealth: {http: http://127.0.0.1:80/}\n", map[string]string{"file-share.container": "[Container]\nImage=img\n"})
	c := "name: t\ntemplate: {registry: builtin, name: t, version: 1}\nafter: [file-share]\nroutes: []\nsecrets: []\ndata:\n  - path: ${DATA_DIR}/t/data\nhealth: {http: http://127.0.0.1:80/}\n"
	for why, unit := range map[string]string{
		":z":                       "Volume=${DATA_DIR}/file-share/data/music:/music:z\nSecurityLabelDisable=true\n",
		"ro,Z":                     "Volume=${DATA_DIR}/file-share/data:/data:ro,Z\nSecurityLabelDisable=true\n",
		"no SecurityLabelDisable":  "Volume=${DATA_DIR}/file-share/data/music:/music:ro\n",
		"z through a values entry": "Volume=${MUSIC}:/music:ro,z\nSecurityLabelDisable=true\n",
	} {
		dir := writeTemplate(t, root, "t", c, map[string]string{"t.container": "[Container]\nImage=img\n" + unit, "values.example.yml": "MUSIC: /mnt/data/stacks/file-share/data/music\n"})
		if lintTemplate(&cliEnv{}, dir) == cli.ExitOK {
			t.Errorf("%s on the share passed lint", why)
		}
	}
	dir := writeTemplate(t, root, "t", c, map[string]string{"t.container": "[Container]\nImage=img\nSecurityLabelDisable=true\nVolume=${DATA_DIR}/file-share/data/music:/music:ro\nVolume=${DATA_DIR}/file-share/data/notes:/notes\nVolume=${DATA_DIR}/t/data:/data:z\n"})
	if lintTemplate(&cliEnv{}, dir) != cli.ExitOK {
		t.Fatal("the share without a relabel and labelling off, the service's own data with :z, is fine")
	}
	// and every built-in that mounts the share
	ymls, _ := filepath.Glob("../../templates/*/laib.yml")
	for _, y := range ymls {
		if lintTemplate(&cliEnv{}, filepath.Dir(y)) != cli.ExitOK {
			t.Errorf("templates/%s does not pass lint", filepath.Base(filepath.Dir(y)))
		}
	}
}
