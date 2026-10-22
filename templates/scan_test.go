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
	contract, err := fs.ReadFile(FS, "scan/laib.yml")
	if err != nil {
		t.Fatal(err)
	}
	paths := regexp.MustCompile(`(?m)^  - path: `).FindAllStringIndex(string(contract), -1)
	if n := strings.Count(string(contract), "backup: false"); len(paths) != 3 || n != len(paths) {
		t.Errorf("%d data paths, %d with backup: false: every one is transient", len(paths), n)
	}
}
