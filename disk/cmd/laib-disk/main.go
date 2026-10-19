// Command laib-disk is the data RAID and the disks' health (#443): `status`,
// `add <device> [--wipe <model>]` (humans only; LAIB_ACTOR) and `probe
// [--hardware]`, the findings laib diag shows, as JSON. The kernel's `laib
// disk` and `laib diag` run it; a person may run it directly.
package main

import (
	"context"
	"encoding/json"
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
	if err := disk.Cmd(ctx, os.Stdout, os.Getenv("LAIB_ACTOR"), jsonOut, args); err != nil {
		fmt.Fprintln(os.Stderr, "laib disk:", err)
		os.Exit(1)
	}
}
