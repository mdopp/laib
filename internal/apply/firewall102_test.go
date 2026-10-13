package apply

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/plan"
	"github.com/mdopp/laib/internal/render"
)

// #102: when the last lan: false port goes (its service removed, or the port
// opened), the ruleset is no longer rendered and the plan deletes the file.
// The table stayed in the kernel and kept dropping the port until a reboot.
func TestTheLastDroppedPortGoingRemovesTheTable(t *testing.T) {
	st, paths, calls := roleBox(t)
	target := plan.Target(paths, "nftables/laib.nft")
	os.MkdirAll(filepath.Dir(target), 0o755)
	os.WriteFile(target, []byte("table inet laib {}\n"), 0o644)
	p := &plan.Plan{Output: render.Output(map[string]string{}), Changes: []plan.Change{{Path: "nftables/laib.nft", Target: target, Kind: "delete"}}}
	if _, err := Run(context.Background(), st, p, paths, map[string]string{}, "probe opens"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(*calls, "\n"), "sudo nft delete table inet laib") {
		t.Fatalf("the table stays in the kernel after its file is gone:\n%s", strings.Join(*calls, "\n"))
	}
}
