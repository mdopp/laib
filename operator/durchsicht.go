package operator

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// The log review (#550 point 6, #551): the runner reads every service's
// warnings and errors of a day, groups them by pattern and keeps what the
// last review saw; the model sees only the real patterns that are new,
// recurring or growing. The journal's err is stderr, and most of it is a
// container's INFO, so a line's content decides, never its priority.

const reviewLines = 5000 // per unit and day; a unit that reaches it is "mindestens"

type logCount struct {
	Unit, Pattern, Example string
	Count                  int
}

// seenPattern is what a review remembers of one unit's pattern.
type seenPattern struct {
	Count int  `json:"count"`
	Told  bool `json:"told"` // shown to a model once; again only when it grows
}

var (
	journalLine = regexp.MustCompile(`^\S+ \S+ [^\s\[:]+(?:\[\d+\])?: (.*)$`)
	// the level a line names: level=x, "level":"x", [x] …
	namedLevel = regexp.MustCompile(`(?i)(?:\blevel=|"level":\s*"|\blvl=|\[)(trace|debug|dbg|info|inf|notice|warn|warning|wrn|error|err|eror|fatal|ftl|crit|critical|panic)\b`)
	// … or as a word in capitals: ERROR (MainThread), INFO:, LOG [Api]
	upperLevel = regexp.MustCompile(`\b(TRACE|DEBUG|VERBOSE|INFO|NOTICE|LOG|WARN|WARNING|ERROR|FATAL|CRITICAL|PANIC)\b`)
	errorWords = regexp.MustCompile(`(?i)\b(error|errors|fatal|panic|exception|failed|failure|timeout|timed out|refused|denied|unauthorized|not connected|aborted|aborting|segfault|cannot|unable)\b|\b[A-Z]\w*(Error|Exception)\b`)
	normalise  = []struct {
		re   *regexp.Regexp
		with string
	}{
		{regexp.MustCompile(`\[?\d{4}[-/]\d{2}[-/]\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z| ?[+-]\d{2}:?\d{2})?\]?`), ""},
		{regexp.MustCompile(`\d{1,2}/\d{1,2}/\d{4},? \d{1,2}:\d{2}:\d{2}(?: [AP]M)?`), ""},
		{regexp.MustCompile(`\[?\b\d{2}:\d{2}:\d{2}(?:[.,]\d+)?\]?`), ""},
		{regexp.MustCompile(`(?:/[\w.@%+-]+){2,}/?`), "<pfad>"},
		{regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`), "<id>"},
		{regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}(?::\d+)?\b`), "<ip>"},
		{regexp.MustCompile(`\b[0-9a-fA-F]{6,}\b`), "<hex>"},
		{regexp.MustCompile(`\d+(?:\.\d+)?`), "<n>"},
		{regexp.MustCompile(`\s+`), " "},
	}
	noiseLevels = []string{"trace", "debug", "dbg", "verbose", "info", "inf", "notice", "log", "warn", "warning", "wrn"}
)

// logPattern is a journal message's pattern and whether it is a real
// error: the level the line names decides; without one, its words do.
// A JSON line (Caddy) is its logger, message and error.
func logPattern(msg string) (string, bool) {
	var j struct{ Level, Logger, Msg, Error string }
	level := ""
	if strings.HasPrefix(msg, "{") && json.Unmarshal([]byte(msg), &j) == nil && j.Msg != "" {
		msg, level = strings.TrimPrefix(j.Logger+": ", ": ")+j.Msg, strings.ToLower(j.Level)
		if j.Error != "" {
			msg += " (" + j.Error + ")"
		}
	} else {
		n, u := namedLevel.FindStringSubmatchIndex(msg), upperLevel.FindStringSubmatchIndex(msg)
		switch {
		case n != nil && (u == nil || n[0] <= u[0]):
			level = strings.ToLower(msg[n[2]:n[3]])
		case u != nil:
			level = strings.ToLower(msg[u[2]:u[3]])
		}
	}
	if slices.Contains(noiseLevels, level) || level == "" && !errorWords.MatchString(msg) {
		return "", false
	}
	for _, r := range normalise {
		msg = r.re.ReplaceAllStringFunc(msg, func(m string) string {
			if r.with == "<hex>" { // an id is hex with a digit and a letter; a number stays a number
				if !strings.ContainsAny(m, "0123456789") || !strings.ContainsAny(strings.ToLower(m), "abcdef") {
					return m
				}
				return "<id>"
			}
			return r.with
		})
	}
	msg = strings.TrimSpace(msg)
	if r := []rune(msg); len(r) > 160 {
		msg = string(r[:160]) + "…"
	}
	return msg, true
}

// review reads the day's logs and returns what the model is shown — ""
// when nothing is new — and remember, which keeps this review for the
// next one; the runner calls it when the session did not fail.
func (o *Operator) review(ctx context.Context, mcp *MCP) (string, func()) {
	text, isErr, err := mcp.Call(ctx, "laib_status", map[string]any{})
	var st struct{ Services []struct{ Name string } }
	if err != nil || isErr || json.Unmarshal([]byte(text), &st) != nil {
		return "Protokoll-Durchsicht: laib status nicht lesbar (" + firstLine(cmp.Or(text, fmt.Sprint(err))) + "). Schreib das in die Notiz.", func() {}
	}
	counts, noise, full := map[string]*logCount{}, map[string]int{}, map[string]bool{}
	var unread []string
	for _, svc := range st.Services {
		out, isErr, err := mcp.Call(ctx, "laib_logs", map[string]any{"args": fmt.Sprintf("%s --since 24h --lines %d --priority warning", svc.Name, reviewLines)})
		var units map[string]string
		if err != nil || isErr || json.Unmarshal([]byte(out), &units) != nil {
			unread = append(unread, svc.Name)
			continue
		}
		for unit, text := range units {
			lines := strings.Split(strings.TrimSpace(text), "\n")
			full[unit] = len(lines) >= reviewLines
			for _, l := range lines {
				m := journalLine.FindStringSubmatch(l)
				if m == nil {
					continue
				}
				p, real := logPattern(m[1])
				if !real {
					noise[unit]++
					continue
				}
				key := unit + "\t" + p
				if counts[key] == nil {
					counts[key] = &logCount{Unit: unit, Pattern: p, Example: m[1]}
				}
				counts[key].Count++
			}
		}
	}
	path := filepath.Join(o.Home, "durchsicht.json")
	last := map[string]seenPattern{}
	if b, err := os.ReadFile(path); err == nil && o.Home != "" {
		json.Unmarshal(b, &last)
	}
	type item struct {
		logCount
		why string
	}
	var news []item
	known := 0
	for key, c := range counts {
		prev, had := last[key]
		switch {
		case !had && c.Count >= 3:
			news = append(news, item{*c, "neu"})
		case had && !prev.Told:
			news = append(news, item{*c, "wiederkehrend"})
		case had && c.Count >= 2*prev.Count && c.Count >= prev.Count+10:
			news = append(news, item{*c, fmt.Sprintf("wächst (vorher %d×)", prev.Count)})
		case had:
			known++
		}
	}
	slices.SortFunc(news, func(a, b item) int {
		return cmp.Or(b.Count-a.Count, strings.Compare(a.Unit+a.Pattern, b.Unit+b.Pattern))
	})
	news = news[:min(len(news), 8)]
	next := map[string]seenPattern{}
	for key, c := range counts {
		next[key] = seenPattern{Count: c.Count, Told: last[key].Told || slices.ContainsFunc(news, func(i item) bool { return i.Unit+"\t"+i.Pattern == key })}
	}
	remember := func() {
		if o.Home != "" {
			os.MkdirAll(o.Home, 0o755)
			b, _ := json.Marshal(next)
			os.WriteFile(path, b, 0o644)
		}
	}
	if len(news) == 0 && len(unread) == 0 {
		return "", remember
	}
	var b strings.Builder
	b.WriteString("Fehlermuster der letzten 24 Stunden (nach Inhalt, nicht nach Journal-Priorität):\n")
	for _, n := range news {
		least := ""
		if full[n.Unit] {
			least = "mindestens "
		}
		ex := n.Example
		if r := []rune(ex); len(r) > 200 {
			ex = string(r[:200]) + "…"
		}
		fmt.Fprintf(&b, "- %s, %s, %s%d×: %s\n  Beispiel: %s\n", n.why, n.Unit, least, n.Count, n.Pattern, ex)
	}
	if known > 0 {
		fmt.Fprintf(&b, "Schon gemeldet und nicht gewachsen: %d Muster (nicht wiederholen).\n", known)
	}
	if len(unread) > 0 {
		fmt.Fprintf(&b, "Logs nicht lesbar: %s.\n", strings.Join(unread, ", "))
	}
	loud := slices.SortedFunc(maps.Keys(noise), func(a, b string) int { return cmp.Or(noise[b]-noise[a], strings.Compare(a, b)) })
	if len(loud) > 0 {
		var l []string
		for _, u := range loud[:min(len(loud), 4)] {
			l = append(l, fmt.Sprintf("%s %d", u, noise[u]))
		}
		fmt.Fprintf(&b, "Rauschen (stderr ohne Fehler, nur gezählt): %s.\n", strings.Join(l, ", "))
	}
	return b.String(), remember
}
