package apply

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// The nightly image run (#528, ADR 0002 amendment): laib-images.timer runs
// `laib updates --run` in the update window after the backup. It pulls the
// images laib keeps current itself (AutoImage); a service whose image moved
// is restarted and verified, and on red it gets its previous image back.
// Every other image changes by proposal. It replaces podman auto-update,
// whose timer stays masked.

// AutoImage: the value key names an image the nightly run takes itself. Its
// container carries the template's AutoUpdate=registry — the mark for "no
// data a new image would migrate" (docs/INTERFACE.md) — and it floats
// (#523: AutoUpdate=registry, :latest or no tag; never a digest).
func AutoImage(svc *state.Service, key string) bool {
	for _, unit := range svc.Units {
		if strings.Contains(unit, "\nImage=${"+key+"}") && strings.Contains(unit, "\nAutoUpdate=registry") {
			return floats(unit, svc.Values[key].Literal)
		}
	}
	return false
}

// ImageRun pulls, restarts, verifies and on red tags the previous image back.
func ImageRun(ctx context.Context, env *cli.Env, st *state.State) int {
	if a := sysd.Active(ctx, "laib-backup.service"); a == "active" || a == "activating" {
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "updates", Result: "info", Detail: "übersprungen: die Sicherung läuft"})
		return cli.ExitOK
	}
	unlock, code := TakeLock(env, st, "updates")
	if code != cli.ExitOK {
		return code
	}
	defer unlock()
	paused := Paused(env.Paths)
	for _, name := range st.Order(st.ServiceNames()) {
		svc := st.Services[name]
		if _, ok := paused[name]; ok {
			continue
		}
		before := map[string]string{} // image → its ID before the pull, for the images that moved
		for _, key := range slices.Sorted(maps.Keys(svc.Values)) {
			img := svc.Values[key].Literal
			if !strings.HasSuffix(key, "IMAGE") || !AutoImage(svc, key) || before[img] != "" {
				continue
			}
			was, _ := sysd.Run(ctx, "podman", "image", "inspect", "--format", "{{.Id}}", img)
			now, err := sysd.Run(ctx, "podman", "pull", "-q", img)
			if err != nil {
				cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "updates", Service: name, Result: "info", Detail: "pull " + img + ": " + cli.Tail(now, 300)})
				continue
			}
			if was, now = strings.TrimSpace(was), strings.TrimSpace(now); was != "" && was != now {
				before[img] = was
			}
		}
		if len(before) == 0 {
			continue
		}
		moved := strings.Join(slices.Sorted(maps.Keys(before)), ", ")
		red := restartVerify(ctx, env, st, name)
		if red == "" {
			cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "updates", Service: name, Result: "green", Detail: "neues Image, grün: " + moved})
			continue
		}
		for img, id := range before {
			sysd.Run(ctx, "podman", "tag", id, img)
		}
		after := restartVerify(ctx, env, st, name)
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "updates", Service: name, Result: "red",
			Detail: fmt.Sprintf("neues Image rot (%s): %s; zurück auf das vorige Image: %s", moved, red, cmp.Or(after, "grün"))})
		code = cli.ExitRed
	}
	return code
}

// restartVerify restarts the service's units and returns its first red check, "" when green;
// a marker announces it (#978).
func restartVerify(ctx context.Context, env *cli.Env, st *state.State, name string) (red string) {
	done := Mark(st, env.Paths, "updates", env.Actor, "neues Image", []string{name}, 10*time.Minute)
	defer func() { done(red == "") }()
	for _, u := range PodRestartSet(st.Services[name].UnitNames()) {
		sysd.Restart(ctx, u)
	}
	for _, c := range Verify(ctx, st, []string{name}) {
		if !c.OK {
			return c.What + " " + c.Detail
		}
	}
	return ""
}
