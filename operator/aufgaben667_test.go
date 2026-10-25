package operator

import "testing"

// #667: the morning report names the due Box-Aufgaben briefly, written by
// the runner, not left to the model; none due, nothing said.
func TestTheReportNamesDueTasks(t *testing.T) {
	list := `{"offen": 3, "faellig": 2, "aufgaben": [
	  {"id": 1, "titel": "Wächter von außen", "erinnern": "2026-12-01"},
	  {"id": 4, "titel": "TOTP für mdopp prüfen", "erinnern": "2026-11-30"}]}`
	if got, want := dueLine(list), "Fällige Box-Aufgaben: #1 Wächter von außen, #4 TOTP für mdopp prüfen."; got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	for _, none := range []string{`{"offen": 2, "faellig": 0, "aufgaben": []}`, "", "laib: unknown tool"} {
		if got := dueLine(none); got != "" {
			t.Errorf("%q: %q", none, got)
		}
	}
}
