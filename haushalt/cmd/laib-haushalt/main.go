// Command laib-haushalt serves the household verbs behind the kernel's verb
// door (ADR 0026, #598): `laib-haushalt [--json] scan …` and `net geraete …`.
// The kernel runs it as laib scan and laib net geraete, after its policy
// check, with LAIB_ACTOR, LAIB_STATE, LAIB_KERNEL and the journal on fd 3.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/mdopp/laib/haushalt"
)

func main() {
	args := os.Args[1:]
	env := &haushalt.Env{Actor: os.Getenv("LAIB_ACTOR"), State: os.Getenv("LAIB_STATE"), Kernel: os.Getenv("LAIB_KERNEL"), In: os.Stdin, Out: os.Stdout, Err: os.Stderr}
	if len(args) > 0 && args[0] == "--json" {
		env.JSON, args = true, args[1:]
	}
	if os.Getenv("LAIB_JOURNAL_FD") == "3" {
		env.Journal = os.NewFile(3, "journal")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := haushalt.Run(ctx, env, args)
	stop()
	os.Exit(code)
}
