package main

import (
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/registry"
)

// #687, the two template updates that failed on the box on 2026-10-25.
// llama-lease mounted a file from laib's config folder and could not read
// it under SELinux (open /presets.json: permission denied, #106 rolled
// back). stimme-whisper's ExecStartPre touched a file in its data folder on
// the host, and the folder belonged to the container's user (touch:
// Permission denied); `podman unshare touch` writes it in podman's user
// namespace, where the host's user may.
func TestLintRefusesTheTwoFailuresOf20261025(t *testing.T) {
	root := t.TempDir()
	c := "name: t\ntemplate: {registry: builtin, name: t, version: 1}\nroutes: []\nsecrets: []\ndata:\n  - path: ${DATA_DIR}/t/data\n  - path: ${DATA_DIR}/t/models\nhealth: {none: test}\n"
	unit := "[Container]\nImage=img\nVolume=${DATA_DIR}/t/data:/config:z\nVolume=${DATA_DIR}/t/models:/models:ro\n"
	for why, extra := range map[string]string{
		"a file from laib's config folder":   "Volume=%h/.config/containers/systemd/t/presets.json:/presets.json:ro\n",
		"a host touch in the mounted folder": "[Service]\nExecStartPre=/usr/bin/touch ${DATA_DIR}/t/data/names.txt\n",
		"a host mkdir, with a prefix":        "[Service]\nExecStartPre=-/usr/bin/mkdir -p ${DATA_DIR}/t/data/cache\n",
		"a redirect in a shell":              "[Service]\nExecStartPre=/bin/sh -c 'test -f ${DATA_DIR}/t/data/x || echo {} >${DATA_DIR}/t/data/x'\n",
		"a write after a check":              "[Service]\nExecStartPost=/bin/sh -c \"sleep 1; cp /etc/hosts ${DATA_DIR}/t/data/hosts\"\n",
	} {
		dir := writeTemplate(t, root, "t", c, map[string]string{"t.container": unit + extra})
		if registry.LintTemplate(&cliEnv{}, dir) == cli.ExitOK {
			t.Errorf("%s passed lint", why)
		}
	}
	// what stays legitimate: the write in podman's user namespace, a read,
	// a write into a folder no container writes (ro) or into the runtime
	// dir, a command inside the container, a host script from laib's
	// config folder (the host reads it, no container does)
	fine := "[Service]\n" +
		"ExecStartPre=/usr/bin/podman unshare touch ${DATA_DIR}/t/data/names.txt\n" +
		"ExecStartPre=/bin/sh -c 'test -f ${DATA_DIR}/t/data/x || find ${DATA_DIR}/t/data -name y 2>/dev/null'\n" +
		"ExecStartPre=/usr/bin/touch ${DATA_DIR}/t/models/.seen\n" +
		"ExecStartPre=/usr/bin/mkdir -p %t/t\n" +
		"ExecStartPost=/usr/bin/podman exec systemd-t touch /config/ready\n" +
		"ExecStartPost=-/bin/sh %h/.config/containers/systemd/t/report.sh\n"
	dir := writeTemplate(t, root, "t", c, map[string]string{"t.container": unit + fine})
	if registry.LintTemplate(&cliEnv{}, dir) != cli.ExitOK {
		t.Fatal("podman unshare, reads, a read-only folder, the runtime dir, podman exec and a host script are fine")
	}
}
