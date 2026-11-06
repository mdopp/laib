package render

import (
	"slices"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// A removed service's data (box.yml backup.keep, 2026-11-06) stay in every
// backup; a folder a service also declares is taken once.
func TestBackupPathsKeepARemovedServicesData(t *testing.T) {
	no := false
	st := &state.State{Box: state.Box{DataDir: "/d", Backup: state.Backup{Keep: []string{"${DATA_DIR}/gesundheit", "${DATA_DIR}/a"}}},
		Services: map[string]*state.Service{"a": {Name: "a", Contract: state.Contract{Data: []state.DataPath{{Path: "${DATA_DIR}/a"}, {Path: "${DATA_DIR}/a/cache", Backup: &no}}}}}}
	got := BackupPaths(st, "/s", "/j")
	if want := []string{"/s", "/j", "/d/a", "/d/gesundheit"}; !slices.Equal(got, want) {
		t.Fatalf("%v, want %v", got, want)
	}
}
