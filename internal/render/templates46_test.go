package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #46: a service with a database never auto-updates — podman's rollback
// restores the image, not the migrated data. Stateless services may.
func TestStatefulTemplatesDoNotAutoUpdate(t *testing.T) {
	// the owner's list (#528): HA with Matter and Z-Wave JS, Immich, Paperless,
	// Vaultwarden, Jellyfin (media), Authelia and LLDAP (auth)
	stateful := []string{"immich", "paperless", "vaultwarden", "home-assistant", "auth", "radicale", "audiobookshelf", "media", "beets", "usage-metrics"}
	for _, name := range stateful {
		units, _ := filepath.Glob(filepath.Join("..", "..", "templates", name, "*.container"))
		if len(units) == 0 {
			t.Fatalf("no units for %s", name)
		}
		for _, u := range units {
			b, _ := os.ReadFile(u)
			if strings.Contains(string(b), "AutoUpdate=registry") {
				t.Errorf("%s auto-updates although it keeps a database", u)
			}
		}
	}
	// #528: AutoUpdate=registry is the mark laib-images.timer keys on (no data
	// a new image would migrate): every container of a service whose data has
	// a database (quiesce: true) lacks it; Laib's own images carry it.
	all, _ := filepath.Glob(filepath.Join("..", "..", "templates", "*", "laib.yml"))
	for _, f := range all {
		b, _ := os.ReadFile(f)
		units, _ := filepath.Glob(filepath.Join(filepath.Dir(f), "*.container"))
		for _, u := range units {
			c, _ := os.ReadFile(u)
			if strings.Contains(string(b), "quiesce: true") && strings.Contains(string(c), "AutoUpdate=registry") {
				t.Errorf("%s auto-updates although its service keeps a database (quiesce: true)", u)
			}
		}
	}
	for _, u := range []string{"haus/haus.container", "frage/frage.container", "bote/bote-persona.container", "bote/bote-automat.container", "werkstatt/werkstatt.container", "caddy/caddy.container", "stimme/stimme-wakeword-trainer.container"} {
		if b, _ := os.ReadFile(filepath.Join("..", "..", "templates", u)); !strings.Contains(string(b), "\nAutoUpdate=registry") {
			t.Errorf("%s: Laib's own image without data must carry AutoUpdate=registry, or nobody updates it (#528)", u)
		}
	}
	for _, name := range []string{"bote", "haus", "werkstatt"} {
		b, _ := os.ReadFile(filepath.Join("..", "..", "templates", name, "laib.yml"))
		if !strings.Contains(string(b), "needs: [mcp]") {
			t.Errorf("%s talks to laib's MCP but does not declare needs: [mcp]", name)
		}
	}
	for _, name := range []string{"mosquitto", "stimme", "werkstatt"} {
		b, _ := os.ReadFile(filepath.Join("..", "..", "templates", name, "laib.yml"))
		if !strings.Contains(string(b), "health:") {
			t.Errorf("%s declares no health at all", name)
		}
	}
}
