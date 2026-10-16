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
