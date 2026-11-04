package journal

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"

	"github.com/mdopp/laib/atomicfile"

	"github.com/mdopp/laib/internal/render"
)

// Known returns the box's secret values (secrets.age), for Append to redact
// before a line is written (#1012). The binary sets it once it knows its
// paths; nil means the patterns alone.
var Known func() map[string]string

// pem* catch key material whatever the tail cut off (#1012): a whole block,
// an END whose BEGIN lies before the cut — the base64 run before it, also
// indented or with escaped newlines — and a BEGIN whose END lies after it.
var (
	pemBlock = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]+-----.*?-----END [A-Z0-9 ]+-----`)
	pemEnd   = regexp.MustCompile(`[A-Za-z0-9+/=\s\\…"']*-----END [A-Z0-9 ]+-----`)
	pemBegin = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]+-----[A-Za-z0-9+/=\s\\…"']*`)
	// token shapes: Laib's and ServiceBay's tokens, an age identity, a
	// bearer, a JWT, and a value after a key, password, secret or token name
	tokenShape = regexp.MustCompile(`\b(?:laib_[a-z0-9-]+_[A-Za-z0-9_-]{16,}|sb_[A-Za-z0-9_-]{16,}|AGE-SECRET-KEY-1[0-9A-Z]{20,}|eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})`)
	bearer     = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]{16,}`)
	keyValue   = regexp.MustCompile(`(?i)((?:^|[^{A-Za-z0-9_])[A-Za-z0-9_]*(?:private[_-]?key|secret|password|passwd|token|api[_-]?key)[A-Za-z0-9_]*["']?\s*[:=]\s*["']?)[A-Za-z0-9+/=_.~-]{12,}`)
)

// Redacted stands where Scrub took something out.
const Redacted = "[geschwärzt]"

// Scrub redacts what must never reach the journal (#1012): the box's known
// secret values the way laib logs does (render.Redact), then key material
// and token shapes, which no list of known values catches (a key rendered
// indented into a config file, a token minted elsewhere).
func Scrub(s string, known map[string]string) string {
	if s == "" {
		return s
	}
	s = render.Redact(render.Output{"s": s}, known)["s"]
	s = pemBlock.ReplaceAllString(s, Redacted)
	s = pemEnd.ReplaceAllString(s, " "+Redacted)
	s = pemBegin.ReplaceAllString(s, Redacted+" ")
	s = tokenShape.ReplaceAllString(s, Redacted)
	s = bearer.ReplaceAllString(s, "${1}"+Redacted)
	return keyValue.ReplaceAllString(s, "${1}"+Redacted)
}

func knownNow() map[string]string {
	if Known == nil {
		return nil
	}
	return Known()
}

func scrubEntry(e *Entry, known map[string]string) bool {
	r, d := Scrub(e.Reason, known), Scrub(e.Detail, known)
	changed := r != e.Reason || d != e.Detail
	e.Reason, e.Detail = r, d
	return changed
}

// scrubMarker records that ScrubOnce ran: the journal is rewritten once,
// Append keeps it clean from then on.
const scrubMarker = ".scrubbed-1012"

// ScrubOnce scrubs every entry already written (#1012), once: each day file
// with a changed line is rewritten through a temp file and a rename, the
// rest untouched. It returns how many entries it changed, never what; a
// second call finds the marker and returns 0.
func ScrubOnce(dir string) (int, error) {
	if _, err := os.Stat(filepath.Join(dir, scrubMarker)); err == nil {
		return 0, nil
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return 0, err
	}
	known, total := knownNow(), 0
	for _, file := range files {
		n, err := scrubFile(file, known)
		total += n
		if err != nil {
			return total, err
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return total, err
	}
	return total, os.WriteFile(filepath.Join(dir, scrubMarker), nil, 0o644)
}

func scrubFile(file string, known map[string]string) (int, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return 0, err
	}
	lines := bytes.SplitAfter(b, []byte("\n"))
	n := 0
	for i, line := range lines {
		body := bytes.TrimSuffix(line, []byte("\n"))
		var e Entry
		if err := json.Unmarshal(body, &e); err != nil { // a broken line is scrubbed as text
			if s := Scrub(string(body), known); s != string(body) {
				lines[i], n = []byte(s+"\n"), n+1
			}
			continue
		}
		if !scrubEntry(&e, known) {
			continue
		}
		out, err := json.Marshal(e)
		if err != nil {
			return 0, err
		}
		lines[i], n = append(out, '\n'), n+1
	}
	if n == 0 {
		return 0, nil
	}
	return n, atomicfile.Write(file+".scrub", file, bytes.Join(lines, nil), 0o644)
}
