package persona

import (
	"strings"
	"testing"
)

// #878: Haus's „Gemeinsam mit Joshua“ opens the Bote with „Aufgabe #N mit
// dir erledigen“. The engine routes it to the model at the top, with every
// tool and the task's number, whatever level the person was on, for the
// admin and the family alike; the next message stays at the top.
func TestATaskConversationStartsAtTheTopWithItsNumber(t *testing.T) {
	for _, sid := range []string{"p", "+4917099999999"} {
		j := term()
		j.Talk(sid, "de", "Menü")
		j.Talk(sid, "de", "1") // a tool's level: the box or the web search
		for _, in := range []string{"Aufgabe #7 mit dir erledigen", "aufgabe 7 mit dir erledigen.", "Aufgabe #7 mit dir erledigen!"} {
			out := j.Talk(sid, "de", in)
			if !strings.HasPrefix(out, "WEITER: -\n") || !strings.Contains(out, "\nAUFGABE: 7") {
				t.Fatalf("%s %q: the model at the top with the task:\n%s", sid, in, out)
			}
		}
		if out := j.Talk(sid, "de", "Ich habe es gemacht"); !strings.HasPrefix(out, "WEITER: -\n") || strings.Contains(out, "AUFGABE") {
			t.Fatalf("%s: the next message stays at the top, without the task line:\n%s", sid, out)
		}
	}
	for _, in := range []string{"Aufgabe mit dir erledigen", "Aufgabe #x mit dir erledigen", "erzähl mir von Aufgabe 7 mit dir erledigen und mehr"} {
		if out := term().Talk("p", "de", in); strings.Contains(out, "AUFGABE:") {
			t.Errorf("%q is no task: %s", in, out)
		}
	}
}
