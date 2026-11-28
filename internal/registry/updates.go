package registry

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/backup"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// laib updates (#287): for every pinned image in the services' values, the
// newest patch and minor release, a tag's or pin's newer digest (#528) and
// the digest an unpinned value runs (Pin, #1130). Nothing pulls it (ADR
// 0034): the Operator's nightly versionen proposes it, with --notes'
// changelog and signature. The kernel reads no answer from the internet
// for it (ADR 0035): it hands the values and what podman knows of them
// locally to laib-updater and prints the updater's rows; what it runs it
// checks itself before an apply (CheckImages). Reading only, except --run.

type ImageUpdate struct {
	Service string `json:"service"`
	Key     string `json:"key"`
	Image   string `json:"image"`
	Current string `json:"current"`
	Patch   string `json:"patch,omitempty"`
	Minor   string `json:"minor,omitempty"`
	Error   string `json:"error,omitempty"`
	Paused  string `json:"paused,omitempty"`  // the host row: the drop-in that pauses OS updates (#486)
	Digest  string `json:"digest,omitempty"`  // a floating tag or a digest pin: the registry's digest, when the box has another (#528)
	Pin     string `json:"pin,omitempty"`     // a floating tag whose digest the box runs: that digest, to pin it (ADR 0034)
	Auto    bool   `json:"auto,omitempty"`    // the laib row: self-update.sh takes it nightly; an image never (ADR 0034)
	Latest  string `json:"latest,omitempty"`  // the laib row (#547): the newest release; never patch or digest, it is no proposal
	At      string `json:"at,omitempty"`      // the laib row: when self-update.sh last looked
	Running string `json:"running,omitempty"` // the laib row (#578): since when laib-images.service runs
	// --notes (ADR 0034), for a new digest: the versions its labels name, its
	// GitHub repository, what changed there (untrusted text, capped); for our
	// own image (ghcr.io/mdopp/) why its signature fails — the updater's
	// word, for the card; the kernel checks again before it runs one
	OldVersion string `json:"oldVersion,omitempty"`
	NewVersion string `json:"newVersion,omitempty"`
	Source     string `json:"source,omitempty"`
	Notes      string `json:"notes,omitempty"`
	Unsigned   string `json:"unsigned,omitempty"`
	// ADR 0037: when a new digest may apply itself (RFC 3339) — our own at
	// once, a third party's after its soak; "" while updates.auto is aus.
	// Issues: the upstream's GitHub issues since the release that name the
	// new version, as the updater counts them, with up to three links
	Ready      string   `json:"ready,omitempty"`
	Issues     int      `json:"issues,omitempty"`
	IssueLinks []string `json:"issueLinks,omitempty"`

	PatchDigest string `json:"patchDigest,omitempty"` // Patch's digest: its proposal keeps the value pinned (#1130)
}

// Pinned is one image value as the kernel hands it to laib-updater
// (updater.Pinned; the two meet over HTTP, never by import): for a value
// without a digest podman's RepoDigests of the local image (Have) and, with
// --notes, its labels.
type Pinned struct {
	Service string            `json:"service"`
	Key     string            `json:"key"`
	Value   string            `json:"value"`
	Have    string            `json:"have,omitempty"`
	Labels  map[string]string `json:"labels,omitempty"`
}

// Call is one question to laib-updater (updater.Call): "updates" [--notes]
// with the values and state/host/image, or "version" <image>.
type Call struct {
	Argv   []string `json:"argv"`
	Images []Pinned `json:"images,omitempty"`
	Host   string   `json:"host,omitempty"`
}

// Ask is how the kernel asks laib-updater, its answer or why there is
// none; main sets it to the door (ADR 0035).
var Ask func(ctx context.Context, env *cli.Env, c Call) (string, error)

var semverTag = regexp.MustCompile(`^v?\d+\.\d+(?:\.\d+)?$`)

// ImageUpdates are the rows laib-updater finds for the box's image values
// and its host image; with notes, a new digest described (ADR 0034).
func ImageUpdates(ctx context.Context, env *cli.Env, st *state.State, notes bool) ([]ImageUpdate, error) {
	c := Call{Argv: []string{"updates"}}
	if notes {
		c.Argv = append(c.Argv, "--notes")
	}
	for _, name := range st.ServiceNames() {
		for _, key := range slices.Sorted(maps.Keys(st.Services[name].Values)) {
			v := st.Services[name].Values[key].Literal
			if !strings.HasSuffix(key, "IMAGE") || v == "" {
				continue
			}
			p := Pinned{Service: name, Key: key, Value: v}
			if !strings.Contains(v, "@") { // what the box runs of a value without a digest, from podman, never the net
				p.Have, _ = sysd.Run(ctx, "podman", "image", "inspect", "--format", "{{json .RepoDigests}}", v)
				if notes {
					out, _ := sysd.Run(ctx, "podman", "image", "inspect", "--format", "{{json .Labels}}", v)
					json.Unmarshal([]byte(out), &p.Labels)
				}
			}
			c.Images = append(c.Images, p)
		}
	}
	if b, err := os.ReadFile(filepath.Join(st.Dir, "host", "image")); err == nil {
		c.Host = string(b)
	}
	if Ask == nil {
		return nil, errors.New("laib-updater ist nicht verdrahtet")
	}
	out, err := Ask(ctx, env, c)
	if err != nil {
		return nil, err
	}
	var ups []ImageUpdate
	if err := json.Unmarshal([]byte(out), &ups); err != nil {
		return nil, fmt.Errorf("laib-updater antwortet unlesbar: %w", err)
	}
	for i := range ups {
		if ups[i].Service == "host" {
			ups[i].Paused = diag.OSPause()
		}
	}
	return ups, nil
}

// hostLine is the host row in words (#486): the pin by digest, whether its
// tag moved, and a pause that holds every OS update.
func hostLine(u ImageUpdate) string {
	what := "aktuell (" + u.Key + ")"
	if u.Patch != "" {
		what = "neuer Digest von " + u.Key + ": " + u.Patch + " (der wöchentliche Lauf schlägt ihn vor)"
	}
	if u.Paused != "" {
		what += "; OS-Updates pausiert durch " + u.Paused
	}
	return strings.TrimSpace(fmt.Sprintf("%-14s %s@%s  %s %s", u.Service, u.Image, u.Current, what, u.Error))
}

// laibRow (#547): Laib itself, from self-update.sh's last run; none before it.
func laibRow(home, running string) (ImageUpdate, bool) {
	r, ok := diag.ReadSelfUpdate(home)
	if !ok {
		return ImageUpdate{}, false
	}
	u := ImageUpdate{Service: "laib", Image: "laib", Current: running, Auto: r.Result != "off", At: r.At.Local().Format("02.01. 15:04")}
	if r.Latest != running {
		u.Latest = r.Latest
	}
	if r.Result == "failed" || r.Result == "skipped" {
		u.Error = r.Detail
	}
	if r.Result == "failed" {
		u.Error = r.Latest + " zurückgenommen: " + r.Detail
	}
	return u, true
}

// laibLine is the laib row in words.
func laibLine(u ImageUpdate) string {
	what := "aktuell"
	switch {
	case u.Latest != "" && u.Auto:
		what = "neu: " + u.Latest + " (nachts automatisch)"
	case u.Latest != "":
		what = "neu: " + u.Latest + " (aus: box.yml updates.laib)"
	}
	if u.Running != "" {
		what = "der Update-Lauf läuft seit " + u.Running + "; " + what
	}
	return strings.TrimSpace(fmt.Sprintf("%-14s %s  %s, geprüft %s  %s", u.Service, u.Current, what, cmp.Or(u.At, "—"), u.Error))
}

// RunningSince (#578): since when laib-images.service runs, "" when it does not.
func RunningSince(ctx context.Context) string {
	if a := sysd.Active(ctx, "laib-images.service"); a != "activating" && a != "active" {
		return ""
	}
	out, _ := sysd.Run(ctx, "systemctl", "--user", "show", "--timestamp=unix", "--value", "-p", "InactiveExitTimestamp", "laib-images.service")
	if n, err := strconv.ParseInt(strings.TrimPrefix(out, "@"), 10, 64); err == nil && n > 0 {
		return time.Unix(n, 0).Local().Format("15:04")
	}
	return "eben"
}

func Updates(ctx context.Context, env *cli.Env, args []string) int {
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	code := cli.ExitOK
	if slices.Contains(args, "--run") && !env.MCP {
		// after Laib's own update (the unit's ExecStartPre): the new release's default verbs as one proposal (#575)
		if _, err := inbox.ProposeDefaults(ctx, env, st); err != nil {
			cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "updates", Result: "red", Detail: "Standard-Verben: " + err.Error()})
		}
		code = apply.ImageRun(ctx, env, st, backup.RcloneImage) // laib-images.timer's nightly run (#528)
	}
	ups, err := ImageUpdates(ctx, env, st, slices.Contains(args, "--notes")) // --notes: the nightly versionen's (ADR 0034)
	if err != nil {                                                          // the night's run is done; only its list is missing
		err = fmt.Errorf("%w — systemctl --user status laib-updater.service zeigt warum", err)
		if slices.Contains(args, "--run") {
			fmt.Fprintln(env.Errw(), "laib:", err)
			return code
		}
		return env.Fail(err)
	}
	u, ok := laibRow(env.Paths.Home, cli.Version)
	if u.Running = RunningSince(ctx); ok || u.Running != "" {
		u.Service, u.Image, u.Current = "laib", "laib", cli.Version
		ups = append(ups, u)
	}
	behind := map[string]string{} // the 30-day yellow in diag (#528): images only
	for _, u := range ups {
		if w := cmp.Or(u.Digest, u.Patch); u.Service != "host" && u.Service != "laib" && w != "" {
			behind[u.Service+" "+u.Key] = w
		}
	}
	diag.SeeBehind(env.Paths.Home, time.Now(), behind)
	ready(env.Paths.Home, st, ups)
	if env.JSON {
		if ups == nil {
			ups = []ImageUpdate{}
		}
		cli.Emit(env, ups)
		return code
	}
	for _, u := range ups {
		if u.Service == "host" && u.Key != "" {
			fmt.Fprintln(env.W(), hostLine(u))
			continue
		}
		if u.Service == "laib" {
			fmt.Fprintln(env.W(), laibLine(u))
			continue
		}
		if u.Digest != "" || !semverTag.MatchString(u.Current) {
			fmt.Fprintf(env.W(), "%-14s %s:%s  %s%s %s\n", u.Service, u.Image, u.Current, cmp.Or(u.Digest, "aktuell"), readyWord(u), u.Error)
			continue
		}
		fmt.Fprintf(env.W(), "%-14s %s:%s  patch %s  minor %s %s\n", u.Service, u.Image, u.Current, cmp.Or(u.Patch, "—"), cmp.Or(u.Minor, "—"), u.Error)
	}
	routineLines(env, st, time.Now())
	return code
}

// ready fills Ready (ADR 0037): from when laib first saw a new digest, a
// third party's after its soak; none while updates.auto is aus.
func ready(home string, st *state.State, ups []ImageUpdate) {
	for i, u := range ups {
		if _, since := diag.Seen(home, u.Service, u.Key); u.Digest != "" && st.Box.Updates.Auto != "aus" {
			if !strings.HasPrefix(u.Image, "ghcr.io/mdopp/") {
				since = since.Add(inbox.Soak(st, u.Service))
			}
			ups[i].Ready = since.UTC().Format(time.RFC3339)
		}
	}
}

// readyWord: whether a new digest applies itself (ADR 0037), in words.
func readyWord(u ImageUpdate) string {
	t, err := time.Parse(time.RFC3339, u.Ready)
	switch {
	case u.Digest == "":
		return ""
	case err != nil:
		return " (als Karte: updates.auto ist aus)"
	case time.Now().Before(t):
		return " (wartet bis " + t.Local().Format("02.01. 15:04") + ", dann von selbst, wenn das Modell es normal findet)"
	}
	return " (spielt sich ein, wenn das Modell es normal findet)"
}

// routineLines (ADR 0037): what applied itself in seven days and how it
// ended, and the new images held for a person, with the model's reason.
func routineLines(env *cli.Env, st *state.State, now time.Time) {
	all, _ := proposal.List(cli.InboxDir(env), false)
	if st.Box.Updates.Auto == "aus" {
		fmt.Fprintln(env.W(), "Updates spielen sich nicht selbst ein (box.yml updates.auto: aus).")
	}
	for _, p := range all {
		images := slices.ContainsFunc(p.Changes, func(c proposal.Change) bool { return strings.HasSuffix(c.Key, "IMAGE") })
		switch {
		case p.Routine != "" && now.Sub(p.Decided) < 7*24*time.Hour:
			fmt.Fprintf(env.W(), "eingespielt    #%d %s, %s: %s\n", p.ID, p.Reason, p.Decided.Local().Format("02.01. 15:04"), cmp.Or(p.Outcome, "fertig"))
		case p.Status == "open" && images:
			fmt.Fprintf(env.W(), "angehalten     #%d %s: %s\n", p.ID, p.Reason, strings.Join(slices.Sorted(maps.Values(p.Notes)), " · "))
		}
	}
}
