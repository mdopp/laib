package main

import (
	"context"
	"errors"
	"os"
	"os/exec"

	"github.com/mdopp/laib/internal/diag"
)

// cmdDisk is `laib disk`: the human's verb (tier apply, the program refuses
// any other actor for add), the work done by laib-disk outside the kernel
// (#443), with laib disk's output and exit code.
func cmdDisk(ctx context.Context, env *cliEnv, args []string) int {
	if env.JSON {
		args = append([]string{"--json"}, args...)
	}
	cmd := exec.CommandContext(ctx, diag.DiskBin(), args...)
	cmd.Env = append(os.Environ(), "LAIB_ACTOR="+env.Actor)
	cmd.Stdout, cmd.Stderr = env.W(), env.Errw()
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	if err != nil {
		return env.Fail(err)
	}
	return 0
}
