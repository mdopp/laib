// Package stick builds a Fedora CoreOS install stick for a Laib box without
// any tool on the laptop: Butane is transpiled in-process, the ISO is
// downloaded and verified, the Ignition is embedded the way coreos-installer
// does it (an xz/gzip cpio in the ISO's IMAGES/IGNITION.IMG area).
package stick

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/coreos/butane/config"
	"github.com/coreos/butane/config/common"
	"github.com/mdopp/laib/usb"
)

// Box is what the wizard asks for.
type Box struct {
	Hostname   string
	Domain     string
	LanIP      string
	SSHKey     string // the public key line
	PassHash   string // crypt(3) hash or ""
	ModelURL   string
	ModelLease string
	GPU        bool
	GHToken    string
	Source     string // base URL serving install-box.sh and the binaries of one build (the VM job); "" = the latest release
}

var placeholder = regexp.MustCompile(`\$\{([A-Z_]+)\}`)

// Render substitutes the template's ${VARS}.
func Render(template string, vars map[string]string) string {
	return placeholder.ReplaceAllStringFunc(template, func(m string) string {
		return vars[m[2:len(m)-1]]
	})
}

// Vars maps a Box onto the template's placeholders.
func (b Box) Vars() map[string]string {
	gpu := "0"
	if b.GPU {
		gpu = "1"
	}
	return map[string]string{
		"SSH_AUTHORIZED_KEY": b.SSHKey, "PASSWORD_HASH": b.PassHash, "HOSTNAME": b.Hostname,
		"DOMAIN": b.Domain, "LAN_IP": b.LanIP, "MODEL_URL": b.ModelURL, "MODEL_LEASE": b.ModelLease,
		"GPU": gpu, "GH_TOKEN": b.GHToken, "SOURCE": b.Source,
	}
}

// Transpile turns Butane YAML into Ignition JSON, strictly and without
// compressing inline files, so the result stays readable.
func Transpile(butane []byte) ([]byte, error) {
	out, report, err := config.TranslateBytes(butane, common.TranslateBytesOptions{TranslateOptions: common.TranslateOptions{NoResourceAutoCompression: true}})
	if err != nil {
		return nil, fmt.Errorf("butane: %w\n%s", err, report.String())
	}
	if report.IsFatal() {
		return nil, fmt.Errorf("butane: %s", report.String())
	}
	var check map[string]any
	if err := json.Unmarshal(out, &check); err != nil {
		return nil, fmt.Errorf("butane produced no JSON: %w", err)
	}
	return out, nil
}

// DestIgnition is the box's Ignition from usb/fedora-coreos.bu.
func DestIgnition(b Box) ([]byte, error) {
	bu := Render(usb.Butane, b.Vars())
	if b.PassHash == "" {
		bu = strings.ReplaceAll(bu, "      password_hash: \"\"\n", "")
	}
	return Transpile([]byte(bu))
}

// ReadSSHKey reads a public key file and returns its first line.
func ReadSSHKey(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0])
	if !strings.HasPrefix(line, "ssh-") && !strings.HasPrefix(line, "ecdsa-") {
		return "", fmt.Errorf("%s does not look like a public key", path)
	}
	return line, nil
}
