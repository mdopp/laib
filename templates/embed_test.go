package templates

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// #140: every template folder is built in; the list in embed.go is by hand,
// and haus and llama were added after it was last edited.
func TestEveryTemplateIsEmbedded(t *testing.T) {
	dirs, _ := filepath.Glob(filepath.Join("*", "laib.yml"))
	if len(dirs) == 0 {
		t.Fatal("no template folders found")
	}
	for _, y := range dirs {
		name := filepath.Dir(y)
		if _, err := fs.Stat(FS, name+"/laib.yml"); err != nil {
			t.Errorf("templates/%s is not embedded: laib add %s fails on a box", name, name)
		}
	}
	if _, err := os.Stat("embed.go"); err != nil {
		t.Fatal(err)
	}
}
