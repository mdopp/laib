package diag

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/mdopp/laib/internal/cli"
)

// `laib auslastung` (read, owner 2026-11-06): the box's values now, for
// Homes' Box → Auslastung. laib-disk measures them (`laib-disk load`, #1047:
// what reads the host's /proc, /sys and nvidia-smi is the hardware program's,
// ADR 0027); the kernel hands it box.yml's dataDir, the GPU and the lease's
// address, and keeps the answer loadWait, so a page that asks every few
// seconds samples the CPU once per window.
var (
	loadWait = 4 * time.Second
	loadMu   sync.Mutex
	loadAt   time.Time
	loadLast json.RawMessage
)

// LoadCmd is the verb: always JSON, laib-disk's as it said it.
func LoadCmd(ctx context.Context, env *cli.Env, _ []string) int {
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	loadMu.Lock()
	defer loadMu.Unlock()
	if loadLast == nil || time.Since(loadAt) > loadWait {
		args := []string{"load", "--data-dir", st.Box.DataDir}
		if st.Box.GPU.Present {
			args = append(args, "--gpu", "--lease", st.ModelLease())
		}
		out, err := exec.CommandContext(ctx, DiskBin(), args...).Output()
		if err != nil || !json.Valid(out) {
			return env.Fail(fmt.Errorf("laib-disk antwortet nicht (%v): die Auslastung ist unbekannt; install-box.sh bringt laib-disk mit (%s)", err, DiskBin()))
		}
		loadLast, loadAt = out, time.Now()
	}
	return cli.Emit(env, loadLast)
}
