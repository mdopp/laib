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
	// (TestHausDocumentsExportNeverStopsTheNotes)
	has("systemd/haus-qmd-refresh.service", "After=haus-documents-export.service\n")
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

// #219: the export must never stop the notes' refresh. It is a oneshot of
// its own that the refresh wants and runs after, whatever its outcome:
// Wants=, never Requires=. That matters most while the box still runs a
// Haus image older than export-documents — after laib applies this and
// before podman auto-update pulls the new image, or if laib is applied
// first. Such a Haus ignores the argument and would start a second
// server; LAIB_TOKEN emptied stops it at once (it requires the token),
// so it fails fast, alone, and red (laib status reads oneshots), and the
// notes are refreshed all the same. A folder Haus cannot write is red the
// same way, and is no reason to stop the notes either.
func TestHausDocumentsExportNeverStopsTheNotes(t *testing.T) {
	st := shippedBox(t)
	out, err := Render(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	execs := func(unit string) []string {
		var steps []string
		for _, m := range regexp.MustCompile(`(?m)^ExecStart\w*=(.*)$`).FindAllStringSubmatch(unit, -1) {
			steps = append(steps, m[1])
		}
		return steps
	}
	refresh, ok := out["systemd/haus-qmd-refresh.service"]
	if !ok {
		t.Fatal("no refresh")
	}
	for _, s := range execs(refresh) {
		if strings.Contains(s, "export-documents") {
			t.Errorf("the export is a step of the refresh: one that fails stops update and embed: %s", s)
		}
	}
	for _, w := range []string{"Wants=haus-documents-export.service\n", "After=haus-documents-export.service\n"} {
		if !strings.Contains(refresh, w) {
			t.Errorf("the refresh lacks %q:\n%s", w, refresh)
		}
	}
	if regexp.MustCompile(`(?m)^(Requires|Requisite|BindsTo|PartOf|Upholds)=.*haus-documents-export`).MatchString(refresh) {
		t.Errorf("the refresh depends on the export succeeding:\n%s", refresh)
	}
	export, ok := out["systemd/haus-documents-export.service"]
	if !ok {
		t.Fatal("systemd/haus-documents-export.service is not rendered")
	}
	if got := execs(export); len(got) != 1 || got[0] != "/usr/bin/podman exec -e LAIB_TOKEN= systemd-haus /haus export-documents" {
		t.Errorf("the export runs %q, want the one step with LAIB_TOKEN emptied", got)
	}
	for _, w := range []string{"Type=oneshot\n", "TimeoutStartSec="} {
		if !strings.Contains(export, w) {
			t.Errorf("the export lacks %q:\n%s", w, export)
		}
	}
	if strings.Contains(export, "[Install]") {
		t.Errorf("the export runs only when the refresh wants it:\n%s", export)
	}
	// nothing backed up covers the documents' folder: no declared path is it or holds it
	for _, p := range BackupPaths(st, "/s", "/j") {
		if p == "/d/haus-dokumente" || strings.HasPrefix("/d/haus-dokumente", strings.TrimSuffix(p, "/")+"/") {
			t.Errorf("the backup takes %s, which holds the documents' cache", p)
		}
	}
}
