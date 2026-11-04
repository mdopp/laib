package pages

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mdopp/laib/atomicfile"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/state"
)

// lanLoop writes the page at the box's bare LAN address (#1043,
// render/lan.go) every render.LanEvery while laib serve runs, from the
// status the other surfaces share (kept). When laib stops, the page keeps
// its Stand and says how old it is.
func lanLoop(ctx context.Context, env *cli.Env) {
	for {
		if st, err := cli.LoadState(env); err == nil && st.Box.Roles.Proxy != "" && st.Box.LanIP != "" {
			r, rows, at := kept(ctx, env, st, false)
			if err := WriteLAN(env, st, r, rows, at, uptime()); err != nil && ctx.Err() == nil {
				fmt.Fprintln(env.Errw(), "laib serve: status page:", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(render.LanEvery):
		}
	}
}

// seen: a service's state and since when, kept across restarts in
// Home/status-seit.json; a state first seen has no since (unknown, never guessed).
type seen struct {
	State string    `json:"state"`
	Since time.Time `json:"since,omitzero"`
}

// WriteLAN writes render/status/index.html.
func WriteLAN(env *cli.Env, st *state.State, r Readiness, rows []ServiceStatus, at time.Time, up time.Duration) error {
	seitFile := filepath.Join(env.Paths.Home, "status-seit.json")
	was, now := map[string]seen{}, map[string]seen{}
	if b, err := os.ReadFile(seitFile); err == nil {
		json.Unmarshal(b, &was)
	}
	s := render.LanStatus{At: at, Version: r.Version, Uptime: up, Problems: len(r.Problems)}
	for _, row := range rows {
		w, ok := was[row.Name]
		if !ok || w.State != row.State {
			w = seen{State: row.State, Since: at}
			if !ok {
				w.Since = time.Time{}
			}
		}
		now[row.Name] = w
		s.Rows = append(s.Rows, render.LanRow{Service: row.Name, State: row.State, Since: w.Since})
	}
	b, _ := json.Marshal(now)
	if err := atomicfile.Write(seitFile+".tmp", seitFile, b, 0o644); err != nil {
		return err
	}
	dir := filepath.Join(env.Paths.Render, "status")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(dir, ".index.html.tmp"), filepath.Join(dir, "index.html"), []byte(render.LanPage(st, &s)), 0o644)
}

// uptime is the host's, from /proc/uptime; 0 when unreadable.
func uptime() time.Duration {
	b, _ := os.ReadFile("/proc/uptime")
	f, _ := strconv.ParseFloat(strings.Fields(string(b) + " 0")[0], 64)
	return time.Duration(f) * time.Second
}
