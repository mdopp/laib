package backup

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// lsNode is one line of `restic ls --json`: the snapshot first, then its nodes.
type lsNode struct {
	StructType string `json:"struct_type"`
	ID         string `json:"id"`
	Type       string `json:"type"`
	Path       string `json:"path"`
	Size       int64  `json:"size"`
}

// probeRestore is the restore test's second half (#552): the latest regular
// snapshot's listing of probe is the truth, never the live file — a database
// changes after its snapshot. That snapshot is restored into scratch with
// `--verify` (restic re-reads every restored file and checks it against the
// blob hashes it recorded), then every file the listing names must be there
// with the recorded size, and a restored SQLite database must be whole. It
// returns what it compared, or why the restore is not usable.
func probeRestore(ctx context.Context, st *state.State, pw, probe, scratch string) (string, error) {
	out, err := ResticRun(ctx, st, pw, "ls", "--json", "--recursive", "--tag", "laib", "latest", probe)
	if err != nil {
		return "", fmt.Errorf("ls %s: %s", probe, cli.Tail(out, 300))
	}
	var snap string
	var files []lsNode
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var n lsNode
		if json.Unmarshal(sc.Bytes(), &n) != nil {
			continue
		}
		if n.StructType == "snapshot" && snap == "" {
			snap = n.ID
		}
		if n.StructType == "node" && n.Type == "file" && (n.Path == probe || strings.HasPrefix(n.Path, strings.TrimSuffix(probe, "/")+"/")) {
			files = append(files, n)
		}
	}
	if snap == "" || len(files) == 0 {
		// an empty listing compares nothing: no green from it
		return "", fmt.Errorf("the latest snapshot holds no file of %s: %s", probe, cli.Tail(out, 200))
	}
	if out, err := ResticRun(ctx, st, pw, "restore", snap, "--include", probe, "--target", scratch, "--verify"); err != nil {
		return "", fmt.Errorf("restore %s: %s", probe, cli.Tail(out, 300))
	}
	var dbs []string
	for _, n := range files {
		got := filepath.Join(scratch, n.Path)
		fi, err := os.Stat(got)
		if err != nil {
			return "", fmt.Errorf("%s is in the snapshot but not in the restore (%v)", n.Path, errors.Unwrap(err))
		}
		if fi.Size() != n.Size {
			return "", fmt.Errorf("%s restored with %d bytes, the snapshot recorded %d", n.Path, fi.Size(), n.Size)
		}
		isDB, err := sqliteWhole(ctx, got)
		if err != nil {
			return "", fmt.Errorf("%s: %w", n.Path, err)
		}
		if isDB {
			dbs = append(dbs, filepath.Base(n.Path))
		}
	}
	detail := fmt.Sprintf("%s: %d files of snapshot %.8s restored, sizes and checksums as recorded", probe, len(files), snap)
	if len(dbs) > 0 {
		detail += "; SQLite whole: " + strings.Join(dbs, ", ")
	}
	return detail, nil
}

// sqliteWhole: a file that starts like an SQLite database must be one. With
// SQLite3 is the binary the probe looks for; a test points it at its own
// stand-in, whatever the machine running the test has installed.
var SQLite3 = "sqlite3"

// a sqlite3 binary on the host that is `PRAGMA integrity_check` answering
// "ok"; without one (Fedora CoreOS has none) it is the header: a valid page
// size and a file that holds every page the header counts — that catches a
// truncated or garbled copy, not a damaged page inside.
func sqliteWhole(ctx context.Context, path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	h := make([]byte, 100)
	n, _ := io.ReadFull(f, h)
	if n < 16 || string(h[:16]) != "SQLite format 3\x00" {
		return false, nil
	}
	if n < 100 {
		return true, errors.New("SQLite header truncated")
	}
	fi, err := f.Stat()
	if err != nil {
		return true, err
	}
	page := int64(binary.BigEndian.Uint16(h[16:18]))
	if page == 1 {
		page = 65536
	}
	if page < 512 || page&(page-1) != 0 || fi.Size()%page != 0 {
		return true, fmt.Errorf("SQLite broken: page size %d, file %d bytes", page, fi.Size())
	}
	if pages := int64(binary.BigEndian.Uint32(h[28:32])); pages > 0 && binary.BigEndian.Uint32(h[24:28]) == binary.BigEndian.Uint32(h[92:96]) && fi.Size() < pages*page {
		return true, fmt.Errorf("SQLite truncated: header counts %d pages, the file holds %d", pages, fi.Size()/page)
	}
	if _, err := exec.LookPath(SQLite3); err == nil {
		out, err := sysd.Run(ctx, "sqlite3", "file:"+path+"?immutable=1", "PRAGMA integrity_check")
		if err != nil || strings.TrimSpace(out) != "ok" {
			return true, fmt.Errorf("PRAGMA integrity_check: %s", cli.Tail(out, 200))
		}
	}
	return true, nil
}
