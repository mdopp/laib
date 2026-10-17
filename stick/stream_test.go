package stick

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

type failAfter struct {
	w    io.WriteCloser
	left int
}

func (f *failAfter) Write(p []byte) (int, error) {
	if f.left <= 0 {
		return 0, errors.New("no space left on device")
	}
	if len(p) > f.left {
		p = p[:f.left]
	}
	n, err := f.w.Write(p)
	f.left -= n
	return n, err
}
func (f *failAfter) Close() error { return f.w.Close() }

// dropper says it wrote everything but keeps only the first half of each
// chunk: a file system that loses data without an error.
type dropper struct{ w io.WriteCloser }

func (d dropper) Write(p []byte) (int, error) {
	d.w.Write(p[:len(p)/2])
	return len(p), nil
}
func (d dropper) Close() error { return d.w.Close() }

// Download hashed what it read from the network, ignored write and close
// errors, and so accepted a truncated ISO on a full disk (#51). It now checks
// every write and re-hashes the file on disk before it accepts it.
func TestDownloadAcceptsOnlyWhatReachedTheDisk(t *testing.T) {
	body := bytes.Repeat([]byte("fedora-coreos "), 300000) // ~4 MB, several chunks
	sum := sha256.Sum256(body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	defer srv.Close()
	art := Artifact{Release: "test", Location: srv.URL, SHA256: hex.EncodeToString(sum[:])}
	saved := createFile
	defer func() { createFile = saved }()
	leftovers := func(dir string) []string {
		var names []string
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return names
	}

	cases := []struct {
		name    string
		create  func(string) (io.WriteCloser, error)
		art     Artifact
		wantErr string
	}{
		{"a clean download", func(n string) (io.WriteCloser, error) { return os.Create(n) }, art, ""},
		{"the disk fills up", func(n string) (io.WriteCloser, error) {
			f, err := os.Create(n)
			return &failAfter{w: f, left: 1 << 20}, err
		}, art, "no space left"},
		{"the disk loses data silently", func(n string) (io.WriteCloser, error) {
			f, err := os.Create(n)
			return dropper{w: f}, err
		}, art, "on disk"},
		{"the server sends something else", func(n string) (io.WriteCloser, error) { return os.Create(n) },
			Artifact{Release: "test", Location: srv.URL, SHA256: strings.Repeat("0", 64)}, "checksum mismatch"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		createFile = c.create
		got, err := Download(c.art, dir, nil)
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.wantErr == "":
			b, _ := os.ReadFile(got)
			if !bytes.Equal(b, body) {
				t.Errorf("%s: the file on disk differs from what was served", c.name)
			}
		case err == nil:
			t.Errorf("%s: accepted, want an error mentioning %q", c.name, c.wantErr)
		case !strings.Contains(err.Error(), c.wantErr):
			t.Errorf("%s: error %q does not mention %q", c.name, err, c.wantErr)
		default:
			if left := leftovers(dir); len(left) != 0 {
				t.Errorf("%s: left %v behind", c.name, left)
			}
		}
	}
}
