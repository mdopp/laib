package apply

import (
	"os/exec"
	"strings"
)

func realRun(args ...string) (string, error) {
	out, err := exec.Command("git", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
