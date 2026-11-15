// Package updater is the program laib-updater (ADR 0035, #1233): the part of
// `laib updates` that talks to the internet. It asks the registries for the
// tags, digests and labels of the box's images, checks our own image's
// signature with cosign and reads changelogs on GitHub, and answers the
// kernel. It runs in its own container (ADR 0027) with nothing of the box:
// no mount, no Laib token, no age key, no state repo; its only credential
// is the door's key, by which it knows a call came from laib. The kernel
// hands it the image values and what podman knows of them locally, and
// trusts its answer for nothing it runs: before an apply it checks the
// digest and our own image's signature itself. It imports nothing of the
// kernel.
package updater

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Call is one question of the kernel (cmd/laib updater.go updaterCall):
// argv "updates" [--notes] with the image values and state/host/image, or
// "version" <image>.
type Call struct {
	Argv   []string `json:"argv"`
	Images []Pinned `json:"images,omitempty"`
	Host   string   `json:"host,omitempty"`
}

// Env is one call's world.
type Env struct {
	GitHub GitHub
}

// Run answers one call: its output and exit code, as the door's reply.
func Run(ctx context.Context, env *Env, c Call) (string, int, error) {
	switch {
	case len(c.Argv) == 2 && c.Argv[0] == "version":
		return ImageVersion(c.Argv[1]), 0, nil
	case len(c.Argv) > 0 && c.Argv[0] == "updates":
		rows := Updates(ctx, env, c.Images, len(c.Argv) > 1 && c.Argv[1] == "--notes")
		if u, ok := Host(ctx, c.Host); ok {
			rows = append(rows, u)
		}
		b, err := json.Marshal(rows)
		return string(b), 0, err
	}
	return "", 2, errors.New("usage: updates [--notes] | version <image> (the kernel asks them for laib updates and a card's versions)")
}

// Handler serves the kernel: POST /call with the door's key, GET /health.
func Handler(key string, env *Env) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"updater": "bereit"})
	})
	mux.HandleFunc("POST /call", func(w http.ResponseWriter, r *http.Request) {
		tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if key == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(key)) != 1 {
			http.Error(w, "unauthorized: only laib calls laib-updater", http.StatusUnauthorized)
			return
		}
		var c Call
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&c); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		out, code, err := Run(r.Context(), env, c)
		msg := ""
		if err != nil {
			msg = fmt.Sprintf("laib: %s\n", err)
		}
		json.NewEncoder(w).Encode(map[string]any{"out": out, "err": msg, "code": code})
	})
	return mux
}
