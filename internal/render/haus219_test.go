package render

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// #219: the memory library indexes Paperless's documents too (haus#13).
// Haus mirrors their OCR text into one folder (`haus export-documents`),
// qmd indexes it read-only as the collection Haus names, and the refresh
// runs the export before qmd reads the folders. The folder is a cache:
// declared, kept out of the backup, beside haus and haus-qmd, never in them.
func TestHausLibraryIndexesTheDocuments(t *testing.T) {
	st := shippedBox(t)
	out, err := Render(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	has := func(file string, want ...string) string {
		t.Helper()
		got, ok := out[file]
		if !ok {
			t.Fatalf("%s is not rendered", file)
		}
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("%s lacks %q:\n%s", file, w, got)
			}
		}
		return got
	}
	// Haus writes the folder (shared label, not :Z: qmd reads it too) and names the collection
	haus := has("quadlet/haus/haus.container", "Environment=HAUS_QMD_DOCUMENTS=dokumente", "Environment=HAUS_DOCUMENTS_DIR=/dokumente",
		"Volume=/d/haus-dokumente:/dokumente:z\n")
	// qmd reads it, read-only
	qmd := has("quadlet/haus/haus-qmd.container", "Volume=/d/haus-dokumente:/dokumente:ro,z\n")
	for file, c := range map[string]string{"haus.container": haus, "haus-qmd.container": qmd} {
		if strings.Contains(c, "haus-dokumente:/dokumente:Z") || strings.Contains(c, "haus-dokumente:/dokumente:ro,Z") {
			t.Errorf("%s labels the shared folder private (:Z); both containers read it", file)
		}
	}
	// index.yml: the documents' collection over the folder, the notes' unchanged
	var cfg struct {
		Collections map[string]struct{ Path, Pattern string }
	}
	if err := json.Unmarshal([]byte(has("quadlet/haus/qmd.json")), &cfg); err != nil {
		t.Fatalf("qmd.json: %v", err)
	}
	if c, ok := cfg.Collections["dokumente"]; !ok || c.Path != "/dokumente" || c.Pattern != "**/*.md" {
		t.Errorf("no dokumente collection over the mounted folder: %+v", cfg.Collections)
	}
	if c := cfg.Collections["notizen"]; c.Path != "/notes" {
		t.Errorf("the notes' collection moved: %+v", cfg.Collections)
	}
	// the refresh: the export first, in Haus's container, then qmd reads both folders
	svc := has("systemd/haus-qmd-refresh.service")
	var steps []string
	for _, m := range regexp.MustCompile(`(?m)^ExecStart=(.*)$`).FindAllStringSubmatch(svc, -1) {
		steps = append(steps, m[1])
	}
	if len(steps) == 0 || steps[0] != "/usr/bin/podman exec systemd-haus /haus export-documents" {
		t.Errorf("the refresh does not export the documents first: %q", steps)
	}
	// a declared data path, not backed up (Paperless is the documents' home), beside haus and haus-qmd
	var declared bool
	for _, d := range st.Services["haus"].Contract.Data {
		if d.Path == "${DATA_DIR}/haus-dokumente" && d.Backup != nil && !*d.Backup {
			declared = true
		}
	}
	if !declared {
		t.Error("${DATA_DIR}/haus-dokumente is not a declared data path with backup: false")
	}
}
