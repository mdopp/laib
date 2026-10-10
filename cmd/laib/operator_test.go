package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

func TestJailKeepsReadsInsideTheServiceData(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "stacks")
	os.MkdirAll(filepath.Join(data, "media", "config"), 0o755)
	os.MkdirAll(filepath.Join(data, "immich"), 0o755)
	os.WriteFile(filepath.Join(data, "immich", "secret.env"), []byte("x"), 0o644)
	os.Symlink(filepath.Join(data, "immich", "secret.env"), filepath.Join(data, "media", "config", "link"))
	st := &state.State{Box: state.Box{DataDir: data}}
	svc := &state.Service{Name: "media", Contract: state.Contract{Data: []state.DataPath{{Path: "${DATA_DIR}/media/config"}}}}
	if _, err := jail(st, svc, filepath.Join(data, "media", "config", "a.yml")); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		filepath.Join(data, "immich", "secret.env"),
		filepath.Join(data, "media", "config", "..", "..", "immich", "secret.env"),
		filepath.Join(data, "media", "config", "link"), // symlink out
		"/etc/passwd",
	} {
		if _, err := jail(st, svc, bad); err == nil {
			t.Fatalf("%s allowed", bad)
		}
	}
	// no data declared: the service's own folder under DATA_DIR
	bare := &state.Service{Name: "beets"}
	if _, err := jail(st, bare, filepath.Join(data, "beets", "config.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := jail(st, bare, filepath.Join(data, "media", "x")); err == nil {
		t.Fatal("other service's folder allowed")
	}
}
