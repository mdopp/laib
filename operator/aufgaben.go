package operator

import (
	"encoding/json"
	"fmt"
	"strings"
)

// dueLine is the morning report's sentence on the due Box-Aufgaben (#667),
// from laib_aufgabe list --faellig; none due, or no answer, says nothing.
func dueLine(list string) string {
	var l struct {
		Aufgaben []struct {
			ID    int    `json:"id"`
			Titel string `json:"titel"`
		} `json:"aufgaben"`
	}
	if json.Unmarshal([]byte(list), &l) != nil || len(l.Aufgaben) == 0 {
		return ""
	}
	var out []string
	for _, a := range l.Aufgaben {
		out = append(out, fmt.Sprintf("#%d %s", a.ID, a.Titel))
	}
	return "Fällige Box-Aufgaben: " + strings.Join(out, ", ") + "."
}
