package journal

import (
	"testing"
	"time"
)

func TestAppendReadLast(t *testing.T) {
	dir := t.TempDir()
	old := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	if err := Append(dir, Entry{T: old, Actor: "laib", Verb: "init", Result: "info"}); err != nil {
		t.Fatal(err)
	}
	if err := Append(dir, Entry{Actor: "claude", Verb: "apply", Service: "media", Result: "green"}); err != nil {
		t.Fatal(err)
	}
	all, err := Read(dir, time.Time{})
	if err != nil || len(all) != 2 {
		t.Fatalf("%v %d", err, len(all))
	}
	recent, _ := Read(dir, time.Now().Add(-time.Hour))
	if len(recent) != 1 || recent[0].Actor != "claude" {
		t.Fatalf("%+v", recent)
	}
	if last, ok := Last(dir); !ok || last.Service != "media" {
		t.Fatalf("%+v", last)
	}
}
