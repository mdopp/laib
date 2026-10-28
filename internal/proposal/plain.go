package proposal

import (
	"regexp"
	"strings"
)

// NotReadable is what a page says when the box's state does not load (#855).
const NotReadable = "Die Box kann ihre Einstellungen gerade nicht lesen. Lade die Seite in einer Minute neu; bleibt es so, steht der Grund unter Details."

// plainErrors are the known raw errors (#855), by their marks (| between
// alternatives), each as one German sentence with the next step. First match wins.
var plainErrors = [][2]string{
	{"gesperrt durch|locked", "Die Box ist gerade mit einer anderen Arbeit beschäftigt. Versuch es in ein paar Minuten noch einmal."},
	{"deadline exceeded|timeout|Timeout", "Das hat zu lange gedauert und wurde abgebrochen. Versuch es in ein paar Minuten noch einmal."},
	{"409|busy", "Das Sprachmodell ist gerade belegt. Versuch es in ein paar Minuten noch einmal."},
	{"connection refused|dial tcp|no such host|unreachable", "Ein Dienst der Box antwortet gerade nicht. Versuch es in ein paar Minuten noch einmal; die Status-Seite zeigt, welcher."},
	{"may not|darf nicht", "Dieses Konto darf das nicht. Frag den, der die Box betreut."},
	{"keine Freigabe von", "Diesen Vorschlag kann die Box nicht zurücknehmen: er wurde nie angewendet. Lade die Inbox neu."},
	{"no such file|does not exist|gibt es nicht", "Diesen Vorschlag gibt es nicht (mehr). Lade die Inbox neu."},
}

// Plain is the sentence a person reads for a raw error line (#855): what it
// means and the next step. The raw line belongs under Details or in the
// journal. A line that is plain already keeps its words and gets the next step.
func Plain(raw string) string {
	raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "laib: "))
	for _, e := range plainErrors {
		for _, m := range strings.Split(e[0], "|") {
			if strings.Contains(raw, m) {
				return e[1]
			}
		}
	}
	if raw == "" || Technical(raw) {
		return "Das hat nicht geklappt. Versuch es noch einmal; klappt es wieder nicht, steht der Grund im Journal."
	}
	return strings.TrimSuffix(raw, ".") + ". Versuch es noch einmal; klappt es wieder nicht, steht mehr im Journal."
}

// technical marks what belongs under Details (#855): a command, a unit,
// an exit code, a Go or HTTP error, English error words, a config file.
var technical = regexp.MustCompile(`\blaib [a-z]|\b[\w@-]+\.(service|timer|socket|container)\b|\bexit (status )?\d|dial tcp|connection refused|no such|deadline exceeded|\b(Get|Post) "|\b(failed|not found|unknown|cannot|refused|invalid|error)\b|\b\w+\.ya?ml\b|(^|\s)--[a-z]|\{"`)

// Technical says whether s shows a person what belongs under Details.
func Technical(s string) bool { return technical.MatchString(s) }
