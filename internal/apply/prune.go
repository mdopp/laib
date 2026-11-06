package apply

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// After a green nightly run (#1071) laib removes the images nothing needs:
// every pull left the one it replaced in podman's storage, ~350 GiB on the
// Kraftwerk's data disk. Kept are an image a container uses (stopped too),
// one a Quadlet unit or an *IMAGE value names (a paused or stopped service
// has no container: Quadlet runs --rm), the extra ones the caller names
// (hooks, rclone), and what the previous green run kept: the image a
// service ran before an update stays one more night, so `laib rollback`
// starts the local copy when the registry does not answer (#370). The run's
// own rollback needs the old image only inside the run. Only `podman rmi`:
// never prune, never a volume.

// keptFile holds the image IDs the last green run kept.
const keptFile = "images-kept.json"

// StoredImage is one image in podman's storage (`podman images --format json`).
type StoredImage struct {
	ID         string   `json:"Id"`
	Names      []string `json:"Names"`
	Containers int      `json:"Containers"`
	Size       int64    `json:"Size"`
}

// StoredImages lists podman's images, for the prune and diag's storage finding.
func StoredImages(ctx context.Context) ([]StoredImage, error) {
	out, err := sysd.Run(ctx, "podman", "images", "--format", "json")
	if err != nil {
		return nil, fmt.Errorf("podman images: %s", cli.Tail(out, 200))
	}
	var imgs []StoredImage
	return imgs, json.Unmarshal([]byte(out), &imgs)
}

// PruneImages removes the images nothing keeps and journals what it freed.
func PruneImages(ctx context.Context, env *cli.Env, st *state.State, extra ...string) {
	imgs, err := StoredImages(ctx)
	if err != nil {
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "updates", Result: "info", Detail: "alte Abbilder nicht entfernt: " + err.Error()})
		return
	}
	keep := map[string]bool{}
	for _, ref := range neededImages(env.Paths.Quadlet, st, extra) {
		if id, err := sysd.Run(ctx, "podman", "image", "inspect", "--format", "{{.Id}}", ref); err == nil {
			keep[strings.TrimSpace(id)] = true
		}
	}
	used, err := sysd.Run(ctx, "podman", "ps", "-a", "--no-trunc", "--format", "{{.ImageID}}")
	if err != nil {
		return // without the containers' images nothing is known to be unused
	}
	for _, id := range strings.Fields(used) {
		keep[id] = true
	}
	var before []string
	b, err := os.ReadFile(filepath.Join(env.Paths.Home, keptFile))
	first := err != nil // the first run only records: what ran before it is unknown
	json.Unmarshal(b, &before)
	now := []string{}
	removed, root := 0, graphRoot(ctx)
	free := freeBytes(root)
	for _, img := range imgs {
		if keep[img.ID] || img.Containers > 0 {
			now = append(now, img.ID)
			continue
		}
		if first || slices.Contains(before, img.ID) {
			continue // one more night for a rollback, then it goes
		}
		// by its names: an ID with two tags is refused without -f, and -f would take a container with it
		if _, err := sysd.Run(ctx, "podman", append([]string{"rmi"}, rmiArgs(img)...)...); err == nil {
			removed++
		}
	}
	if b, err := json.Marshal(now); err == nil {
		os.WriteFile(filepath.Join(env.Paths.Home, keptFile), b, 0o644)
	}
	if removed == 0 {
		return
	}
	detail := fmt.Sprintf("%d alte Abbilder entfernt", removed)
	if after := freeBytes(root); after > free {
		detail = "Alte Abbilder entfernt: " + Gigabytes(after-free) + " frei"
	}
	cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "updates", Result: "green", Detail: detail})
}

// neededImages: every Image= of a Quadlet unit on the box, every *IMAGE
// value (an image a hook script runs), and the extra ones.
func neededImages(quadlet string, st *state.State, extra []string) []string {
	refs := slices.Clone(extra)
	filepath.WalkDir(quadlet, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".container") {
			b, _ := os.ReadFile(path)
			refs = append(refs, imageOf(string(b)))
		}
		return nil
	})
	for _, svc := range st.Services {
		for k, v := range svc.Values {
			if strings.HasSuffix(k, "IMAGE") {
				refs = append(refs, v.Literal)
			}
		}
	}
	return slices.DeleteFunc(refs, func(r string) bool { return r == "" || strings.Contains(r, "${") })
}

func rmiArgs(img StoredImage) []string {
	if len(img.Names) == 0 {
		return []string{img.ID}
	}
	return img.Names
}

func graphRoot(ctx context.Context) string {
	out, _ := sysd.Run(ctx, "podman", "info", "--format", "{{.Store.GraphRoot}}")
	return strings.TrimSpace(out)
}

func freeBytes(path string) uint64 {
	var fs syscall.Statfs_t
	if path == "" || syscall.Statfs(path, &fs) != nil {
		return 0
	}
	return fs.Bavail * uint64(fs.Bsize)
}

// Gigabytes is b as people read it: "12 GB", "1,5 GB", "300 MB".
func Gigabytes(b uint64) string {
	switch {
	case b < 1<<30:
		return fmt.Sprintf("%d MB", b>>20)
	case b < 10<<30:
		return strings.Replace(fmt.Sprintf("%.1f GB", float64(b)/(1<<30)), ".", ",", 1)
	}
	return fmt.Sprintf("%d GB", b>>30)
}
