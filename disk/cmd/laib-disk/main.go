// Command laib-disk is the data RAID and the disks' health (#443): `status`,
// `add <device> [--wipe <model>]` (humans only; LAIB_ACTOR) and `probe
// [--hardware]`, the findings laib diag shows, as JSON; and `load --data-dir
// <dir> [--gpu] [--lease <url>]`, the box's values laib auslastung shows, as
// JSON (#1047). The kernel's `laib disk`, `laib diag` and `laib auslastung`
// run it; a person may run it directly.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/mdopp/laib/disk"
)

func main() {
	args := os.Args[1:]
	jsonOut := len(args) > 0 && args[0] == "--json"
	if jsonOut {
		args = args[1:]
	}
	ctx := context.Background()
	if len(args) > 0 && args[0] == "probe" {
		json.NewEncoder(os.Stdout).Encode(disk.Probe(ctx, len(args) > 1 && args[1] == "--hardware"))
		return
	}
	if len(args) > 0 && args[0] == "load" {
		fs := flag.NewFlagSet("load", flag.ExitOnError)
		data, gpu, lease := fs.String("data-dir", "", "box.yml dataDir"), fs.Bool("gpu", false, "the box has a GPU"), fs.String("lease", "", "the model lease's address")
		fs.Parse(args[1:])
		json.NewEncoder(os.Stdout).Encode(disk.Measure(ctx, *data, *gpu, *lease))
		return
	}
	if err := disk.Cmd(ctx, os.Stdout, os.Getenv("LAIB_ACTOR"), jsonOut, args); err != nil {
		fmt.Fprintln(os.Stderr, "laib disk:", err)
		os.Exit(1)
	}
}
