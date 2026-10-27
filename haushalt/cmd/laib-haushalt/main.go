// Command laib-haushalt serves the household verbs behind the kernel's verb
// door (ADR 0026, #598) in its own container (ADR 0027): HTTP on
// HAUSHALT_LISTEN (127.0.0.1:5902), each call with the door's key
// (HAUSHALT_KEY); a proposal goes back to laib at LAIB_MCP_URL with
// LAIB_TOKEN, the haushalt hand's; laib tell reaches bote-notify at
// BOTE_NOTIFY_URL with BOTE_NOTIFY_TOKEN, both rendered only with a Bote (#676);
// laib wish files its issue with GITHUB_INTAKE_TOKEN, rendered only with box.yml
// intake (#797), at GITHUB_API_URL (default api.github.com).
package main

import (
	"cmp"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/mdopp/laib/haushalt"
)

func main() {
	key := os.Getenv("HAUSHALT_KEY")
	if key == "" {
		log.Fatal("laib-haushalt: HAUSHALT_KEY is empty: anyone on the box could call it as laib's door")
	}
	addr := cmp.Or(os.Getenv("HAUSHALT_LISTEN"), "127.0.0.1:5902")
	laib := haushalt.MCP{URL: os.Getenv("LAIB_MCP_URL"), Token: os.Getenv("LAIB_TOKEN")}
	log.Printf("laib-haushalt: %s, laib at %s", addr, laib.URL)
	srv := &http.Server{Addr: addr, Handler: haushalt.Handler(key, laib, haushalt.Bote{URL: os.Getenv("BOTE_NOTIFY_URL"), Token: os.Getenv("BOTE_NOTIFY_TOKEN")},
		haushalt.GitHub{API: os.Getenv("GITHUB_API_URL"), Token: os.Getenv("GITHUB_INTAKE_TOKEN")}), ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
