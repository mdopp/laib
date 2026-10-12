// Package journal is the append-only record: one JSONL file per day.
package journal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Entry is one journal line (docs/STATE.md).
type Entry struct {
	T       time.Time `json:"t"`
	Actor   string    `json:"actor"`
	Verb    string    `json:"verb"`
	Service string    `json:"service,omitempty"`
	Reason  string    `json:"reason,omitempty"`
	Commit  string    `json:"commit,omitempty"`
	Result  string    `json:"result"` // green | red | info
	Detail  string    `json:"detail,omitempty"`
	MS      int64     `json:"ms,omitempty"`
}

// Append writes one line to today's file.
func Append(dir string, e Entry) error {
	if e.T.IsZero() {
		e.T = time.Now().UTC()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, e.T.UTC().Format("2006-01-02")+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

// Read returns entries since a time, oldest first.
func Read(dir string, since time.Time) ([]Entry, error) {
	es, _, err := ReadCounted(dir, since)
	return es, err
}

// ReadCounted reads like Read and also counts the lines it could not parse:
// one broken line (a hand's note, a crash mid-write) must not hide a whole
// day, and the caller that budgets by the journal decides what the count
// means (#35). A line too long for the scanner or an unreadable file is an
// error, never silently the end of the day.
func ReadCounted(dir string, since time.Time) ([]Entry, int, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return nil, 0, err
	}
	sort.Strings(files)
	var out []Entry
	skipped := 0
	for _, file := range files {
		day := strings.TrimSuffix(filepath.Base(file), ".jsonl")
		if d, err := time.Parse("2006-01-02", day); err == nil && d.Add(24*time.Hour).Before(since) {
			continue
		}
		f, err := os.Open(file)
		if err != nil {
			return nil, skipped, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			var e Entry
			if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
				skipped++
				continue
			}
			if !e.T.Before(since) {
				out = append(out, e)
			}
		}
		if err := sc.Err(); err != nil {
			f.Close()
			return nil, skipped, fmt.Errorf("%s: %w", file, err)
		}
		f.Close()
	}
	return out, skipped, nil
}

// Last returns the newest entry, if any.
func Last(dir string) (Entry, bool) {
	es, err := Read(dir, time.Time{})
	if err != nil || len(es) == 0 {
		return Entry{}, false
	}
	return es[len(es)-1], true
}
