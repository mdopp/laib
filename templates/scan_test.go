package templates

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// #568: the scan service mounts two folders of the shared file-share tree
// (its inbox, Paperless' consume folder); relabelling them would walk the
// whole share on every start and fail on an entry core does not own (#230).
// Everything it keeps is transient: the documents live in Paperless.
func TestScanNeverRelabelsTheShareAndKeepsNothingInTheBackup(t *testing.T) {
	unit, err := fs.ReadFile(FS, "scan/scan.container")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range regexp.MustCompile(`(?m)^Volume=(\S+)$`).FindAllStringSubmatch(string(unit), -1) {
		if strings.Contains(m[1], "/file-share/") && strings.Contains(m[1], ":z") {
			t.Errorf("%s relabels the share", m[1])
		}
	}
	if !strings.Contains(string(unit), "SecurityLabelDisable=true") || !strings.Contains(string(unit), "Network=host") {
		t.Error("the share mounts need SecurityLabelDisable, the SANE scanner and the lease the host network")
	}
	// an AirScan device's name has spaces: unquoted, systemd splits it into assignments (#571)
	if !strings.Contains(string(unit), `Environment="SCANNER=${SCANNER}"`) {
		t.Error("SCANNER is not quoted in the unit")
	}
	contract, err := fs.ReadFile(FS, "scan/laib.yml")
	if err != nil {
		t.Fatal(err)
	}
	paths := regexp.MustCompile(`(?m)^  - path: `).FindAllStringIndex(string(contract), -1)
	if n := regexp.MustCompile(`(?m)^    backup: false`).FindAllStringIndex(string(contract), -1); len(paths) != 5 || len(n) != 4 || !strings.Contains(string(contract), "  - path: ${DATA_DIR}/dokumente ") {
		t.Errorf("%d data paths, %d with backup: false: all transient but the helper's record in ${DATA_DIR}/dokumente", len(paths), len(n))
	}
}

// ADR 0028: the program is Dokumente's image; Paperless' hook reaches it on 8720 from its pod,
// which the LAN never does and Caddy never routes; laib-notify's eingang is mounted without a
// relabel (laib-notify's own :Z labels it); the two tokens are the scan service's secrets.
func TestScanRunsDokumenteWithItsHookOffTheLAN(t *testing.T) {
	unit, _ := fs.ReadFile(FS, "scan/scan.container")
	contract, _ := fs.ReadFile(FS, "scan/laib.yml")
	values, _ := fs.ReadFile(FS, "scan/values.example.yml")
	for _, want := range []string{"Image=${DOKUMENTE_IMAGE}", "Environment=DOKUMENTE_LISTEN=0.0.0.0:8720",
		"Environment=PAPERLESS_TOKEN=${PAPERLESS_TOKEN}", "Environment=DOKUMENTE_WEBHOOK_TOKEN=${DOKUMENTE_WEBHOOK_TOKEN}",
		"\nVolume=/mnt/data/laib/notify/eingang:/meldungen\n", "AddDevice=/dev/bus/usb", "Network=host",
		"\nVolume=${DATA_DIR}/dokumente:/dokumente:z\n", "\nEnvironment=VERTRAEGE_FILE=/dokumente/laib-vertraege.json\n"} {
		if !strings.Contains(string(unit), want) {
			t.Errorf("scan.container lacks %q", want)
		}
	}
	if !strings.Contains(string(contract), "- {port: 8720, lan: false}") || !strings.Contains(string(contract), "routes: []") {
		t.Error("8720 must be declared lan: false, and scan has no route")
	}
	for _, s := range []string{"PAPERLESS_TOKEN", "DOKUMENTE_WEBHOOK_TOKEN"} {
		if !strings.Contains(string(contract), "- name: "+s) || !strings.Contains(string(values), "!secret "+s) {
			t.Errorf("%s is not the scan service's secret", s)
		}
	}
	if !strings.Contains(string(values), "DOKUMENTE_IMAGE: ghcr.io/mdopp/dokumente:latest") {
		t.Error("the image is not Dokumente's")
	}
}
