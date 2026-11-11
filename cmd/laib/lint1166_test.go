package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/registry"
)

// #1166: a template container on the host network, with labelling off or
// without the hardening lines says why in laib.yml exceptions:, under its
// file name; a reason for what the unit does not do, or for no unit, is
// stale. The count of each kind is scripts/ratchet.py's.
func TestLintWantsAReasonForEachException(t *testing.T) {
	head := "name: t\ntemplate: {registry: builtin, name: t, version: 1}\nroutes: []\nsecrets: []\ndata: []\nhealth: {none: \"nothing to probe here\"}\n"
	host := "[Container]\nImage=img\nNetwork=host\nSecurityLabelDisable=true\n"
	write := func(contract, unit string) string {
		dir := filepath.Join(t.TempDir(), "t")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "laib.yml"), []byte(contract), 0o644)
		os.WriteFile(filepath.Join(dir, "t.container"), []byte(unit), 0o644)
		return dir
	}
	reasons := "exceptions:\n  t.container:\n    hostNetwork: \"mDNS on the LAN\"\n    labelOff: \"the USB stick (#417)\"\n    unhardened: \"unknown, kept until measured\"\n"
	if registry.LintTemplate(&cliEnv{}, write(head+"hostNetwork: true\n"+reasons, host)) != cli.ExitOK {
		t.Fatal("each exception with its reason is fine")
	}
	for why, contract := range map[string]string{
		"no reasons":              head + "hostNetwork: true\n",
		"no reason for the label": head + "hostNetwork: true\nexceptions:\n  t.container:\n    hostNetwork: \"mDNS on the LAN\"\n    unhardened: \"unknown, kept until measured\"\n",
		"two words are no reason": head + "hostNetwork: true\nexceptions:\n  t.container:\n    hostNetwork: \"mDNS on the LAN\"\n    labelOff: \"the USB stick (#417)\"\n    unhardened: \"not measured\"\n",
		"an unknown kind":         head + "hostNetwork: true\n" + reasons + "    privileged: \"it was so in ServiceBay\"\n",
		"a reason for no unit":    head + "hostNetwork: true\n" + reasons + "  gone.container:\n    unhardened: \"unknown, kept until measured\"\n",
	} {
		if registry.LintTemplate(&cliEnv{}, write(contract, host)) == cli.ExitOK {
			t.Errorf("%s passed lint", why)
		}
	}
	// the default: a pod's container with the three lines needs no reason, and a stale one is refused
	hard := "[Container]\nImage=img\nReadOnly=true\nNoNewPrivileges=true\nDropCapability=ALL\n"
	if registry.LintTemplate(&cliEnv{}, write(head, hard)) != cli.ExitOK {
		t.Fatal("a hardened container needs no exceptions")
	}
	if registry.LintTemplate(&cliEnv{}, write(head+"exceptions:\n  t.container:\n    unhardened: \"unknown, kept until measured\"\n", hard)) == cli.ExitOK {
		t.Error("a reason for hardening the unit has passed lint: the count fell, the reason must go")
	}
	if registry.LintTemplate(&cliEnv{}, write(head, "[Container]\nImage=img\nReadOnly=true\nNoNewPrivileges=true\n")) == cli.ExitOK {
		t.Error("a container without DropCapability=all passed lint without a reason")
	}
}
