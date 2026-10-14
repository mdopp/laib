package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #169: the trainer's state reaches laib status (Stimme's row) and diag,
// so Haus's Box page can show it through the haus hand (haus#15).
func TestWakewordStateFromTheTrainersFiles(t *testing.T) {
	data := t.TempDir()
	micro := filepath.Join(data, "stimme", "micro")
	write := func(name, body string) {
		t.Helper()
		if err := os.MkdirAll(micro, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(micro, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if d, f := wakewordState(data, "hey_joshua", "Hey Joshua"); !strings.Contains(d, "noch nicht trainiert") || f != nil {
		t.Fatalf("nothing yet: %q %+v", d, f)
	}
	write("hey_joshua.status.json", `{"state": "training", "phrase": "Hey Joshua", "rev": "r1", "reason": "", "at": "2026-10-14T08:00:00Z"}`)
	if d, f := wakewordState(data, "hey_joshua", "Hey Joshua"); !strings.Contains(d, "wird trainiert") || f != nil {
		t.Fatalf("training: %q %+v", d, f)
	}
	write("hey_joshua.status.json", `{"state": "failed", "phrase": "Hey Joshua", "rev": "r1", "reason": "GPU nicht gefunden", "at": "2026-10-14T09:00:00Z"}`)
	d, f := wakewordState(data, "hey_joshua", "Hey Joshua")
	if !strings.Contains(d, "fehlgeschlagen") || !strings.Contains(d, "GPU nicht gefunden") {
		t.Fatalf("failed: %q", d)
	}
	if f == nil || f.Severity != "gelb" || f.Service != "stimme" || !strings.Contains(f.Fix, "persona") {
		t.Fatalf("a failed training is a yellow finding with the way out: %+v", f)
	}
	write("hey_joshua.tflite", "model")
	write("hey_joshua.json", "{}")
	if d, f := wakewordState(data, "hey_joshua", "Hey Joshua"); !strings.Contains(d, "„Hey Joshua“ trainiert") || f != nil {
		t.Fatalf("a model on disk is trained, whatever the status says: %q %+v", d, f)
	}
}
