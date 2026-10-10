package stick

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mdopp/laib/usb"
)

// LiveIgnition is the Ignition the live ISO boots with: it carries the
// box's Ignition as a file, the disk-picking pre-install, the EFI
// post-install, and a unit that runs coreos-installer and reboots. This is
// what `coreos-installer iso customize --dest-ignition --pre-install
// --post-install` builds; here it is Butane we control.
func LiveIgnition(dest []byte, hostname string) ([]byte, error) {
	var compact map[string]any
	if err := json.Unmarshal(dest, &compact); err != nil {
		return nil, err
	}
	destJSON, _ := json.Marshal(compact)
	bu := `variant: fcos
version: 1.5.0
storage:
  files:
    - path: /etc/laib/dest.ign
      mode: 0600
      contents:
        inline: ` + yamlQuote(string(destJSON)) + `
    - path: /usr/local/bin/laib-pre-install.sh
      mode: 0755
      contents:
        inline: ` + yamlBlock(usb.PreInstall) + `
    - path: /usr/local/bin/laib-post-install.sh
      mode: 0755
      contents:
        inline: ` + yamlBlock(usb.PostInstall) + `
    - path: /usr/local/bin/laib-install.sh
      mode: 0755
      contents:
        inline: |
          #!/usr/bin/env bash
          # The whole install, unattended: pick the disk, write CoreOS with the
          # box's Ignition, promote the disk in the EFI order, reboot.
          set -euo pipefail
          export SERVER_NAME=` + yamlQuote(hostname) + `
          exec > >(tee /dev/tty1 /dev/console 2>/dev/null) 2>&1 || true
          echo "laib: installing — do not switch off"
          /usr/local/bin/laib-pre-install.sh
          coreos-installer install --ignition-file /etc/laib/dest.ign
          /usr/local/bin/laib-post-install.sh || true
          echo "laib: installed; rebooting in 5 s — remove the stick when the screen goes dark"
          sleep 5
          systemctl reboot
systemd:
  units:
    - name: laib-install.service
      enabled: true
      contents: |
        [Unit]
        Description=Laib: install Fedora CoreOS to the box's disk
        After=network-online.target coreos-installer-pre.target
        Wants=network-online.target
        ConditionPathExists=/etc/laib/dest.ign
        OnFailure=emergency.target

        [Service]
        Type=oneshot
        ExecStart=/usr/local/bin/laib-install.sh
        StandardOutput=journal+console

        [Install]
        WantedBy=multi-user.target
`
	return Transpile([]byte(bu))
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
