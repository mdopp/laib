package render

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// #219: the memory library indexes Paperless's documents too (haus#13).
// Homes mirrors their OCR text into one folder (`homes export-documents`),
// qmd indexes it read-only as the collection Homes names, and the refresh
// runs the export before qmd reads the folders. The folder is a cache:
// declared, kept out of the backup, beside homes and homes-qmd, never in them.
func TestHomesLibraryIndexesTheDocuments(t *testing.T) {
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
	// Homes writes the folder (shared label, not :Z: qmd reads it too) and names the collection
	homes := has("quadlet/homes/homes.container", "Environment=HOMES_QMD_DOCUMENTS=dokumente", "Environment=HOMES_DOCUMENTS_DIR=/dokumente",
		"Volume=/d/homes-dokumente:/dokumente:z\n")
	// qmd reads it, read-only
	qmd := has("quadlet/homes/homes-qmd.container", "Volume=/d/homes-dokumente:/dokumente:ro,z\n")
	for file, c := range map[string]string{"homes.container": homes, "homes-qmd.container": qmd} {
		if strings.Contains(c, "homes-dokumente:/dokumente:Z") || strings.Contains(c, "homes-dokumente:/dokumente:ro,Z") {
			t.Errorf("%s labels the shared folder private (:Z); both containers read it", file)
		}
	}
	// index.yml: the documents' collection over the folder, the notes' unchanged
	var cfg struct {
		Collections map[string]struct{ Path, Pattern string }
	}
	if err := json.Unmarshal([]byte(has("quadlet/homes/qmd.json")), &cfg); err != nil {
		t.Fatalf("qmd.json: %v", err)
	}
	if c, ok := cfg.Collections["dokumente"]; !ok || c.Path != "/dokumente" || c.Pattern != "**/*.md" {
		t.Errorf("no dokumente collection over the mounted folder: %+v", cfg.Collections)
	}
	if c := cfg.Collections["notizen"]; c.Path != "/notes" {
		t.Errorf("the notes' collection moved: %+v", cfg.Collections)
	}
	// the refresh: the export first, in Homes's container, then qmd reads both folders
	// (TestHomesDocumentsExportNeverStopsTheNotes)
	has("systemd/homes-qmd-refresh.service", "After=homes-documents-export.service\n")
	// a declared data path, not backed up (Paperless is the documents' home), beside homes and homes-qmd
	var declared bool
	for _, d := range st.Services["homes"].Contract.Data {
		if d.Path == "${DATA_DIR}/homes-dokumente" && d.Backup != nil && !*d.Backup {
			declared = true
		}
	}
	if !declared {
		t.Error("${DATA_DIR}/homes-dokumente is not a declared data path with backup: false")
	}
}

// #219: the export must never stop the notes' refresh. It is a oneshot of
// its own that the refresh wants and runs after, whatever its outcome:
// Wants=, never Requires=. That matters most while the box still runs a
// Homes image older than export-documents — after laib applies this and
// before podman auto-update pulls the new image, or if laib is applied
// first. Such a Homes ignores the argument and would start a second
// server; LAIB_TOKEN emptied stops it at once (it requires the token),
// so it fails fast, alone, and red (laib status reads oneshots), and the
// notes are refreshed all the same. A folder Homes cannot write is red the
// same way, and is no reason to stop the notes either.
func TestHomesDocumentsExportNeverStopsTheNotes(t *testing.T) {
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
	refresh, ok := out["systemd/homes-qmd-refresh.service"]
	if !ok {
		t.Fatal("no refresh")
	}
	for _, s := range execs(refresh) {
		if strings.Contains(s, "export-documents") {
			t.Errorf("the export is a step of the refresh: one that fails stops update and embed: %s", s)
		}
	}
	for _, w := range []string{"Wants=homes-documents-export.service\n", "After=homes-documents-export.service\n"} {
		if !strings.Contains(refresh, w) {
			t.Errorf("the refresh lacks %q:\n%s", w, refresh)
		}
	}
	if regexp.MustCompile(`(?m)^(Requires|Requisite|BindsTo|PartOf|Upholds)=.*homes-documents-export`).MatchString(refresh) {
		t.Errorf("the refresh depends on the export succeeding:\n%s", refresh)
	}
	export, ok := out["systemd/homes-documents-export.service"]
	if !ok {
		t.Fatal("systemd/homes-documents-export.service is not rendered")
	}
	if got := execs(export); len(got) != 1 || got[0] != "/usr/bin/podman exec -e LAIB_TOKEN= systemd-homes /homes export-documents" {
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
		if p == "/d/homes-dokumente" || strings.HasPrefix("/d/homes-dokumente", strings.TrimSuffix(p, "/")+"/") {
			t.Errorf("the backup takes %s, which holds the documents' cache", p)
		}
	}
}
