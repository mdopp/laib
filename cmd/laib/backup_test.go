package main

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/state"
)

func TestBackupPathsFollowDeclarations(t *testing.T) {
	st, err := state.Load(filepath.Join("..", "..", "testdata", "golden", "basic", "state"))
	if err != nil {
		t.Fatal(err)
	}
	no := false
	st.Services["whoami"].Contract.Data = []state.DataPath{{Path: "${DATA_DIR}/whoami/bulk", Backup: &no}, {Path: "${DATA_DIR}/whoami/cfg"}}
	got := render.BackupPaths(st, "/s", "/j")
	want := []string{"/s", "/j", "/mnt/data/stacks/authelia/config", "/mnt/data/stacks/caddy/data", "/mnt/data/stacks/whoami/cfg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
}
