// Package haushalt is the program laib-haushalt (ADR 0026, #598): the
// household verbs that read what the door hands over, leave an order for a
// service, file a proposal or tell one person something — scan, net geraete
// and tell. It runs in its own
// container (ADR 0027): the kernel checks hand, tier and verb, then hands
// each call over HTTP on 127.0.0.1 with the caller's actor, the verb's
// ceiling and the facts the verbs need. A proposal goes back to laib over
// MCP with the program's own token, the haushalt hand's, which only
// proposes. It imports nothing of the kernel and reads none of its files.
package haushalt

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Exit codes, as the kernel's (docs/INTERFACE.md).
const (
	exitOK      = 0
	exitRefused = 3
)

// Call is one verb the door hands over (cmd/laib/door.go doorCall).
type Call struct {
	Argv    []string     `json:"argv"`
	JSON    bool         `json:"json"`
	Actor   string       `json:"actor"`
	Ceiling string       `json:"ceiling"`
	LanIP   string       `json:"lanIp"`
	Scan    *ScanService `json:"scan,omitempty"`
}

// ScanService is the service that provides scan, as laib reads it from its state:
// its status file's content (nil while it wrote none) and its orders
// folder, mounted into this container at the same path ("" without a
// status file: not set up).
type ScanService struct {
	Name   string  `json:"name"`
	Status *string `json:"status"`
	Orders string  `json:"orders"`
}

// Laib is the way back: a proposal as the haushalt hand, under a ceiling.
type Laib interface {
	Propose(ctx context.Context, ceiling string, args ...string) (id int, class string, err error)
}

// Env is one call's world.
type Env struct {
	Call
	Laib Laib
	Bote Bote // tell's way to a person (#676)
	Out  io.Writer
	Err  io.Writer
	// Journal gets one line per entry; the kernel writes each as the caller
	Journal io.Writer
}

// Run is one verb: scan …, net geraete … or tell ….
func Run(ctx context.Context, env *Env, args []string) int {
	switch {
	case len(args) > 0 && args[0] == "scan":
		return Scan(ctx, env, args[1:])
	case len(args) > 1 && args[0] == "net" && args[1] == "geraete":
		return NetDevices(env, args[2:])
	case len(args) > 0 && args[0] == "tell":
		return Tell(ctx, env, args[1:])
	}
	return env.fail(errors.New("usage: scan … | net geraete … | tell … (the kernel serves them as laib scan, laib net geraete, laib tell)"))
}

// Handler serves the door: POST /call with the door's key, GET /health.
func Handler(key string, laib Laib, bote Bote) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"haushalt": "bereit"})
	})
	mux.HandleFunc("POST /call", func(w http.ResponseWriter, r *http.Request) {
		tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if key == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(key)) != 1 {
			http.Error(w, "unauthorized: only laib's verb door calls laib-haushalt", http.StatusUnauthorized)
			return
		}
		var c Call
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&c); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var out, errb, jr bytes.Buffer
		code := Run(r.Context(), &Env{Call: c, Laib: laib, Bote: bote, Out: &out, Err: &errb, Journal: &jr}, c.Argv)
		lines := []json.RawMessage{}
		for _, l := range bytes.Split(bytes.TrimSpace(jr.Bytes()), []byte("\n")) {
			if json.Valid(l) {
				lines = append(lines, l)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"out": out.String(), "err": errb.String(), "code": code, "journal": lines})
	})
	return mux
}

func (e *Env) fail(err error) int {
	fmt.Fprintln(e.Err, "laib:", strings.TrimSpace(err.Error()))
	return 1
}

func (e *Env) refused(msg string) int {
	fmt.Fprintln(e.Err, "laib:", msg)
	return exitRefused
}

func (e *Env) emit(v any) int {
	enc := json.NewEncoder(e.Out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return e.fail(err)
	}
	return exitOK
}

// journal hands one line to the kernel; it sets actor and verb itself.
func (e *Env) journal(service, reason, detail string) {
	if e.Journal != nil {
		b, _ := json.Marshal(map[string]string{"service": service, "reason": reason, "result": "info", "detail": detail})
		e.Journal.Write(append(b, '\n'))
	}
}

// writeAtomic is boxfs.WriteAtomic's shape (#148), which this program may
// not import: the temp file synced before the rename, the directory after.
func writeAtomic(tmp, path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
