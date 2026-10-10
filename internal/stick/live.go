package stick

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mdopp/laib/usb"
)

// LiveIgnition is the Ignition the live ISO boots with. It is what
// `coreos-installer iso customize --dest-ignition --pre-install
// --post-install` builds (src/live/customize.rs), reproduced here so the
// ISO's own coreos-installer.service does the install and the reboot:
//
//   - /etc/coreos/dest.ign                      the box's Ignition
//   - /etc/coreos/installer.d/0000-customize.yaml  ignition-file + a placeholder
//     dest-device that the pre-install hook overrides with 0050-dest-device.yaml
//   - pre-install-laib.service  RequiredBy=coreos-installer.service, runs
//     usb/pre-install.sh (smallest non-removable disk, hostname for DHCP)
//   - post-install-laib.service RequiredBy=coreos-installer.target, runs
//     usb/post-install.sh (EFI boot order; harmless without EFI)
func LiveIgnition(dest []byte, hostname string) ([]byte, error) {
	var compact map[string]any
	if err := json.Unmarshal(dest, &compact); err != nil {
		return nil, err
	}
	destJSON, _ := json.Marshal(compact)
	pre := withEnv(usb.PreInstall, "SERVER_NAME", hostname)
	bu := `variant: fcos
version: 1.5.0
storage:
  files:
    - path: /etc/coreos/dest.ign
      mode: 0600
      contents:
        inline: ` + yamlQuote(string(destJSON)) + `
    - path: /etc/coreos/installer.d/0000-customize.yaml
      mode: 0600
      contents:
        inline: |
          ignition-file: /etc/coreos/dest.ign
          dest-device: /dev/disk/by-id/placeholder-overridden-by-pre-install
    - path: /usr/local/bin/pre-install-laib
      mode: 0700
      contents:
        inline: ` + yamlBlock(pre) + `
    - path: /usr/local/bin/post-install-laib
      mode: 0700
      contents:
        inline: ` + yamlBlock(usb.PostInstall) + `
systemd:
  units:
    - name: pre-install-laib.service
      enabled: true
      contents: |
        [Unit]
        Description=Pre-Install Script (laib): pick the OS disk
        After=coreos-installer-pre.target
        Before=coreos-installer.service

        [Service]
        Type=oneshot
        ExecStart=/usr/local/bin/pre-install-laib
        RemainAfterExit=true
        StandardOutput=kmsg+console
        StandardError=kmsg+console

        [Install]
        RequiredBy=coreos-installer.service
    - name: post-install-laib.service
      enabled: true
      contents: |
        [Unit]
        Description=Post-Install Script (laib): EFI boot order
        After=coreos-installer.service
        Before=coreos-installer.target

        [Service]
        Type=oneshot
        ExecStart=/usr/local/bin/post-install-laib
        RemainAfterExit=true
        StandardOutput=kmsg+console
        StandardError=kmsg+console

        [Install]
        RequiredBy=coreos-installer.target
`
	return Transpile([]byte(bu))
}

// withEnv puts `export NAME=value` right after the script's shebang.
func withEnv(script, name, value string) string {
	lines := strings.SplitN(script, "\n", 2)
	export := fmt.Sprintf("export %s=%s", name, shellQuote(value))
	if len(lines) == 2 && strings.HasPrefix(lines[0], "#!") {
		return lines[0] + "\n" + export + "\n" + lines[1]
	}
	return "#!/usr/bin/env bash\n" + export + "\n" + script
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func yamlQuote(s string) string {
	b, _ := json.Marshal(s) // a JSON string is a valid YAML double-quoted scalar
	return string(b)
}

func yamlBlock(s string) string {
	var b strings.Builder
	b.WriteString("|\n")
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		fmt.Fprintf(&b, "          %s\n", line)
	}
	return strings.TrimRight(b.String(), "\n")
}
