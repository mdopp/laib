package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/backup"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/state"
)

// sqliteFile is a database of pages pages of 4096 bytes whose header counts
// counted pages (a valid header: change counter = version-valid-for).
func sqliteFile(pages, counted int) []byte {
	b := make([]byte, pages*4096)
	copy(b, "SQLite format 3\x00")
	binary.BigEndian.PutUint16(b[16:], 4096)
	binary.BigEndian.PutUint32(b[24:], 7)
	binary.BigEndian.PutUint32(b[28:], uint32(counted))
	binary.BigEndian.PutUint32(b[92:], 7)
	return b
}

// #552: the monthly restore test compared the restored file with the live
// one, so a database (LLDAP's users.db, changed by every login) was always
// red. It compares with the snapshot now: restic's listing of the snapshot
// (path, size), a restore with --verify (restic's own checksums), and a
// restored SQLite database must be whole. Each way a restore can be broken
// turns it red.
func TestTheRestoreTestComparesWithTheSnapshotNotTheLiveFile(t *testing.T) {
	b := newRbox(t)
	// the header checks first, whether or not this machine has sqlite3 (CI's runner has)
	realSQLite := backup.SQLite3
	t.Cleanup(func() { backup.SQLite3 = realSQLite })
	backup.SQLite3 = "laib-test-no-sqlite3"
	st, err := cli.LoadState(b.env)
	if err != nil {
		t.Fatal(err)
	}
	var probe string
	for _, name := range st.Order(st.ServiceNames()) {
		if ps := state.SnapshotPaths(st, st.Services[name]); len(ps) > 0 {
			probe = ps[0]
			break
		}
	}
	db := sqliteFile(3, 3)
	snapshot := map[string][]byte{"users.db": db, "lldap_config.toml": []byte("[ldap]\n")}
	// the live database moved on after the snapshot: that is no failure
	os.MkdirAll(probe, 0o755)
	os.WriteFile(filepath.Join(probe, "users.db"), sqliteFile(4, 4), 0o644)

	realRestic := backup.ResticRun
	t.Cleanup(func() { backup.ResticRun = realRestic })
	var restored map[string][]byte // what the restore writes; nil = what the snapshot has
	var calls []string
	failRestore := "" // restic's output when the restore itself fails
	listing := func() string {
		lines := []string{`{"time":"2026-10-22T03:06:00Z","id":"abcdef0123456789","short_id":"abcdef01","struct_type":"snapshot"}`,
			`{"name":"lldap","type":"dir","path":"` + probe + `","struct_type":"node"}`}
		for name, raw := range snapshot {
			n, _ := json.Marshal(map[string]any{"name": name, "type": "file", "path": filepath.Join(probe, name), "size": len(raw), "struct_type": "node"})
			lines = append(lines, string(n))
		}
		return strings.Join(lines, "\n")
	}
	backup.ResticRun = func(_ context.Context, _ *state.State, _ string, args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		switch args[0] {
		case "ls":
			if snapshot == nil {
				return `{"id":"abcdef0123456789","struct_type":"snapshot"}`, nil
			}
			return listing(), nil
		case "restore":
			if failRestore != "" {
				return failRestore, errors.New("exit status 1")
			}
			target := args[slices.Index(args, "--target")+1]
			files := restored
			if files == nil {
				files = snapshot
			}
			for name, raw := range files {
				p := filepath.Join(target, probe, name)
				os.MkdirAll(filepath.Dir(p), 0o755)
				os.WriteFile(p, raw, 0o644)
			}
		}
		return "ok", nil
	}
	check := func() (int, journal.Entry) {
		t.Helper()
		calls = nil
		os.RemoveAll(b.env.Paths.Journal)
		rc := backup.Cmd(context.Background(), b.env, []string{"check"})
		es, _ := journal.Read(b.env.Paths.Journal, time.Time{})
		if len(es) == 0 {
			t.Fatal("the restore test wrote no journal line")
		}
		return rc, es[len(es)-1]
	}

	if rc, e := check(); rc != cli.ExitOK || e.Result != "green" {
		t.Fatalf("a restore that matches its snapshot is green although the live users.db differs: rc %d %+v\n%s", rc, e, strings.Join(calls, "\n"))
	}
	if j := strings.Join(calls, "\n"); !strings.Contains(j, "restore abcdef0123456789 --include "+probe+" --target ") || !strings.Contains(j, "--verify") {
		t.Fatalf("the restore must be the listed snapshot, verified by restic:\n%s", j)
	}
	// #633 (vm-install, recovery): the scratch folder's parents carry the
	// host's SELinux label, which restic may not remove; only the security.*
	// xattrs are left alone, every other restore error stays red
	if restore := calls[slices.IndexFunc(calls, func(c string) bool { return strings.HasPrefix(c, "restore ") })]; !strings.HasSuffix(restore, " --exclude-xattr security.* --verify") {
		t.Fatalf("the restore must leave the security xattrs alone and nothing else: %s", restore)
	}
	for _, c := range []struct {
		name     string
		restored map[string][]byte
		snapshot map[string][]byte
		want     string
	}{
		{"a file restored with another size", map[string][]byte{"users.db": db, "lldap_config.toml": []byte("[ldap]\nx\n")}, nil, "recorded"},
		{"a file missing from the restore", map[string][]byte{"users.db": db}, nil, "not in the restore"},
		{"a truncated database", map[string][]byte{"users.db": sqliteFile(2, 3), "lldap_config.toml": []byte("[ldap]\n")}, map[string][]byte{"users.db": sqliteFile(2, 3), "lldap_config.toml": []byte("[ldap]\n")}, "SQLite truncated"},
		{"a garbled database", map[string][]byte{"users.db": append(sqliteFile(2, 2), 1, 2, 3), "lldap_config.toml": []byte("[ldap]\n")}, map[string][]byte{"users.db": append(sqliteFile(2, 2), 1, 2, 3), "lldap_config.toml": []byte("[ldap]\n")}, "SQLite broken"},
		{"a snapshot without the probe's files", nil, map[string][]byte{}, "holds no file"},
	} {
		restored, snapshot = c.restored, c.snapshot
		if snapshot == nil {
			snapshot = map[string][]byte{"users.db": db, "lldap_config.toml": []byte("[ldap]\n")}
		}
		if rc, e := check(); rc == cli.ExitOK || e.Result != "red" || !strings.Contains(e.Detail, c.want) {
			t.Errorf("%s must turn the restore test red with %q: rc %d %+v", c.name, c.want, rc, e)
		}
	}

	// restic's own error stays red: a blob that does not match, past the xattr filter
	restored, snapshot = nil, map[string][]byte{"users.db": db, "lldap_config.toml": []byte("[ldap]\n")}
	failRestore = "ignoring error for /data/auth/lldap/users.db: verify: hash mismatch\nFatal: There were 1 errors"
	if rc, e := check(); rc == cli.ExitOK || e.Result != "red" || !strings.Contains(e.Detail, "There were 1 errors") {
		t.Errorf("a failed restore must turn the restore test red: rc %d %+v", rc, e)
	}
	failRestore = ""

	// with a sqlite3 binary on the host, PRAGMA integrity_check decides
	restored, snapshot = nil, map[string][]byte{"users.db": db, "lldap_config.toml": []byte("[ldap]\n")}
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "sqlite3"), []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	backup.SQLite3 = "sqlite3"
	if rc, e := check(); rc == cli.ExitOK || !strings.Contains(e.Detail, "integrity_check") {
		t.Errorf("a database sqlite3 does not call ok must be red: rc %d %+v", rc, e)
	}
}
