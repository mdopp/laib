package diag

import "testing"

// #746: what a repair does, in the owner's words, from finding and verb.
func TestARepairSaysWhatItDoes(t *testing.T) {
	for _, c := range []struct {
		f    Finding
		want string
	}{
		{Finding{Service: "paperless", Repair: []string{"secret", "rotate", "PAPERLESS_DB_PASSWORD"}}, "Das Passwort der Paperless-Datenbank wird erneuert; danach prüft die Box, ob Paperless läuft"},
		{Finding{Service: "jellyfin", Repair: []string{"secret", "rotate", "JELLYFIN_ADMIN_PASSWORD"}}, "Das Admin-Passwort von Jellyfin wird erneuert; danach prüft die Box, ob Jellyfin läuft"},
		{Finding{Service: "authelia", Repair: []string{"secret", "rotate", "AUTHELIA_STORAGE_KEY"}}, "Das Geheimnis AUTHELIA_STORAGE_KEY von Authelia wird erneuert; danach prüft die Box, ob Authelia läuft"},
		{Finding{Service: "mosquitto", Repair: []string{"secret", "zurueck", "MQTT_PASSWORD"}}, "Das Geheimnis MQTT_PASSWORD von Mosquitto bekommt den Wert vor der Erneuerung zurück"},
		{Finding{Service: "stimme", Repair: []string{"flash", "esphome"}, Does: "„Hey Joshua“ wird auf den Lautsprecher gespielt"}, "„Hey Joshua“ wird auf den Lautsprecher gespielt"},
		{Finding{Repair: []string{"flash", "esphome"}}, "Neue Firmware von Esphome wird auf das Gerät gespielt"},
		{Finding{Repair: []string{"restart", "home-assistant"}}, "Home Assistant wird neu gestartet"},
		{Finding{Repair: []string{"servicebay", "remove"}}, "Der alte ServiceBay-Ordner wird gelöscht (vorher gesichert)"},
		{Finding{Repair: []string{"servicebay", "remove", "--trotzdem"}}, "Der alte ServiceBay-Ordner wird gelöscht (vorher gesichert)"},
		{Finding{Repair: []string{"apply"}}, "Die Box wird wieder auf den gespeicherten Stand gebracht"},
		{Finding{Repair: []string{"backup"}}, "Die Box macht jetzt eine Sicherung"},
		{Finding{Repair: []string{"backup", "check"}}, "Die Box prüft ihre Sicherung und stellt probeweise daraus wieder her"},
		{Finding{Repair: []string{"operator", "enable"}}, "Der Operator wird eingeschaltet, mit einem frischen Zugang"},
	} {
		if got := does(c.f); got != c.want {
			t.Errorf("%v: %q, want %q", c.f.Repair, got, c.want)
		}
	}
}
