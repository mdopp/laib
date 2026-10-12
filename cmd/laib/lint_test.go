package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTemplate: a minimal template folder; extra files on top.
func writeTemplate(t *testing.T, root, name, contract string, files map[string]string) string {
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

const lintContract = "name: %s\ntemplate: {registry: builtin, name: %s, version: 1}\nroutes: []\nsecrets: []\ndata:\n  - path: ${DATA_DIR}/%s/data\nhealth: {http: http://127.0.0.1:80/}\n"

func TestLintRefusesAnUndeclaredBindSource(t *testing.T) {
	root := t.TempDir()
	c := "name: t\ntemplate: {registry: builtin, name: t, version: 1}\nroutes: []\nsecrets: []\ndata:\n  - path: ${DATA_DIR}/t/data\nhealth: {http: http://127.0.0.1:80/}\n"
	dir := writeTemplate(t, root, "t", c, map[string]string{"t.container": "[Container]\nImage=img\nVolume=${DATA_DIR}/t/data:/data:z\nVolume=${DATA_DIR}/t/config:/config:ro,z\n"})
	if lintTemplate(dir) == exitOK {
		t.Fatal("a Volume= source that is not a declared data path passed lint (#36: podman refuses a missing bind source)")
	}
	writeTemplate(t, root, "t", c, map[string]string{"t.container": "[Container]\nImage=img\nVolume=${DATA_DIR}/t/data:/data:z\nVolume=${DATA_DIR}/t/data/sub:/sub:z\nVolume=/run/dbus:/run/dbus:ro\nVolume=/mnt/data/laib/render/t:/laib:ro,z\nVolume=named:/v\n"})
	if lintTemplate(dir) != exitOK {
		t.Fatal("declared paths, their subfolders, host runtime paths, rendered files and named volumes are fine")
	}
}

func TestLintAcceptsANeighboursDeclaredPath(t *testing.T) {
	root := t.TempDir()
	writeTemplate(t, root, "share", "name: share\ntemplate: {registry: builtin, name: share, version: 1}\nroutes: []\nsecrets: []\ndata:\n  - path: ${DATA_DIR}/share/data/music\nhealth: {http: http://127.0.0.1:80/}\n", map[string]string{"share.container": "[Container]\nImage=img\n"})
	c := "name: t\ntemplate: {registry: builtin, name: t, version: 1}\nafter: [share]\nroutes: []\nsecrets: []\ndata:\n  - path: ${DATA_DIR}/t/data\nhealth: {http: http://127.0.0.1:80/}\n"
	dir := writeTemplate(t, root, "t", c, map[string]string{"t.container": "[Container]\nImage=img\nVolume=${DATA_DIR}/share/data/music:/music:ro,z\n"})
	if lintTemplate(dir) != exitOK {
		t.Fatal("a path the after: neighbour declares is created by its apply and may be mounted")
	}
	writeTemplate(t, root, "t", c, map[string]string{"t.container": "[Container]\nImage=img\nVolume=${DATA_DIR}/share/data/photos:/photos:ro,z\n"})
	if lintTemplate(dir) == exitOK {
		t.Fatal("a path nobody declares passed lint")
	}
}

func TestLintRefusesAHookTheImageCannotRun(t *testing.T) {
	root := t.TempDir()
	c := "name: t\ntemplate: {registry: builtin, name: t, version: 1}\nroutes: []\nsecrets: []\ndata: []\nhealth: {http: http://127.0.0.1:80/}\n"
	dir := writeTemplate(t, root, "t", c, map[string]string{"t.container": "[Container]\nImage=img\n", "pre-start": "#!/usr/bin/env perl\nprint 1\n"})
	if lintTemplate(dir) == exitOK {
		t.Fatal("a perl hook passed lint; the hooks image has python3, bash and sh")
	}
	writeTemplate(t, root, "t", c, map[string]string{"pre-start": "#!/usr/bin/env python3\nprint(1)\n", "post-deploy": "#!/usr/bin/env bash\necho 1\n"})
	if lintTemplate(dir) != exitOK {
		t.Fatal("python3 and bash hooks are what the image runs")
	}
}
