package diag

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/mdopp/laib/internal/proposal"
)

// says are what a finding means to a person (#855), by probe: one plain
// German sentence for the red mail and a repair's card; Detail and Fix
// stay for the admin. %s is the service as people call it.
var says = map[string]string{
	"unit":         "%s läuft nicht.",
	"health":       "%s antwortet nicht.",
	"status":       "%s meldet ein Problem.",
	"drift":        "Auf der Box weicht etwas von ihren Einstellungen ab; Laib gleicht es beim Aufräumen an.",
	"disk":         "Eine Festplatte der Box wird voll.",
	"box":          "Die Box ist nicht ganz so eingerichtet, wie Laib es braucht.",
	"boot-check":   "Ein misslungenes System-Update nimmt die Box nicht von selbst zurück.",
	"firewall":     "Eine zweite Firewall sperrt die Dienste der Box aus dem Heimnetz aus.",
	"nftables":     "Die Firewall der Box ist nicht geladen; Laib lädt sie beim nächsten Aufräumen.",
	"watchdog":     "Fällt die Box aus, merkt es niemand von außen.",
	"backup":       "Mit der Sicherung stimmt etwas nicht.",
	"recovery":     "Das Wiederherstellungsblatt ist noch nicht als gesichert bestätigt.",
	"restore-test": "Ob sich die Sicherung zurückholen lässt, wurde länger nicht geprüft.",
	"raid":         "Wie es den Festplatten geht, weiß die Box gerade nicht genau.",
	"acme":         "Ein Zertifikat der Box kann sich vielleicht nicht erneuern.",
	"boot":         "Für das nächste System-Update ist zu wenig Platz.",
	"operator":     "Der Operator, der nachts nach der Box sieht, arbeitet nicht richtig.",
	"secret":       "Geräte von %s kennen ein erneuertes Geheimnis noch nicht.",
	"updates":      "Für %s liegt ein Update bereit.",
	"os-update":    "Mit dem System-Update der Box stimmt etwas nicht.",
	"self-update":  "Laib bringt sich nicht von selbst auf den neuesten Stand.",
	"skill":        "Eine Fähigkeit des Operators braucht Aufmerksamkeit.",
	"schluessel":   "%s braucht einen Schlüssel von dir.",
}

// Say is f in one plain sentence (#855). A service's own status line
// speaks for itself when it is plain.
func Say(f Finding) string {
	who := cmp.Or(proposal.Named(f.Service), "Ein Dienst")
	switch {
	case f.Probe == "status" && f.Detail != "" && !proposal.Technical(f.Detail):
		return who + ": " + f.Detail
	case f.Probe == "drift" && f.Severity == "info":
		return "Auf der Box läuft etwas, das Laib nicht verwaltet."
	case f.Probe == "updates" && f.Service == "":
		return "Die Box hält ihre Programme nicht von selbst aktuell."
	}
	s, ok := says[f.Probe]
	if !ok {
		s = map[string]string{"rot": "Auf der Box ist etwas nicht in Ordnung.", "gelb": "Auf der Box braucht etwas Aufmerksamkeit."}[f.Severity]
	}
	if strings.Contains(s, "%s") {
		s = fmt.Sprintf(s, who)
	}
	return cmp.Or(s, "Ein Hinweis der Box.")
}
