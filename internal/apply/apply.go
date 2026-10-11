// Package apply makes the box match the state: snapshot, write, reload,
// restart, verify. A red verify is a red result, never a retry.
package apply

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/plan"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// Check is one verification step.
type Check struct {
	Service string `json:"service"`
	What    string `json:"what"`
	OK      bool   `json:"ok"`
	Detail  string `json:"detail,omitempty"`
}

// Result is what apply reports.
type Result struct {
	Commit     string            `json:"commit"`
	Written    []string          `json:"written"`
	Restarted  []string          `json:"restarted"`
	Checks     []Check           `json:"checks"`
	Green      bool              `json:"green"`
	NewSecrets map[string]string `json:"-"` // emitted by post-deploy hooks; the caller stores them
}

// Run applies a computed plan. The snapshot is a Git commit of the state repo
// (the data-volume snapshot comes with backup, later); every written file is
// written atomically.
func Run(ctx context.Context, st *state.State, p *plan.Plan, paths boxfs.Paths, secrets map[string]string, reason string) (Result, error) {
	res := Result{NewSecrets: map[string]string{}}
	if len(p.Conflicts) > 0 {
		return res, fmt.Errorf("unit name conflict, remove the other source first: %s", strings.Join(p.Conflicts, "; "))
	}
	commit, err := snapshot(ctx, paths.State, reason)
	if err != nil {
		return res, fmt.Errorf("snapshot: %w", err)
	}
	res.Commit = commit
	// activation owed from here until verify ran (#39): an apply killed in
	// between is finished by the next apply or converge
	if err := WritePending(paths, p.Services, reason); err != nil {
		return res, fmt.Errorf("pending marker: %w", err)
	}

	// role outputs are validated before anything is written (#50): a
	// Caddyfile a snippet broke never reaches the box
	for _, c := range p.Changes {
		if c.Kind == "delete" {
			continue
		}
		if err := ValidateOutput(ctx, st, c.Path, p.Output[c.Path]); err != nil {
			return res, err
		}
	}
	previousImages := map[string]string{} // change path → Image= on the box before this apply
	for _, c := range p.Changes {
		if strings.HasSuffix(c.Path, ".container") {
			if b, err := os.ReadFile(c.Target); err == nil {
				previousImages[c.Path] = imageOf(string(b))
			}
		}
	}
	for _, c := range p.Changes {
		if c.Kind == "delete" {
			// a unit whose file goes must stop first: after daemon-reload systemd
			// forgets it, and a running container would keep running unmanaged;
			// a host unit is disabled too, or it is back on the next boot (#41)
			if unit := unitOfFile(c.Path); unit != "" {
				sysd.Stop(ctx, unit)
				if strings.HasPrefix(c.Path, "systemd/") {
					sysd.Run(ctx, "systemctl", "--user", "disable", unit)
				}
			}
			if err := os.Remove(c.Target); err != nil && !errors.Is(err, os.ErrNotExist) {
				return res, err
			}
			if strings.HasPrefix(c.Path, "quadlet/") {
				os.Remove(filepath.Dir(c.Target)) // the folder, once empty
			}
			res.Written = append(res.Written, c.Target+" (removed)")
			continue
		}
		if err := writeAtomic(c.Target, p.Output[c.Path]); err != nil {
			return res, err
		}
		res.Written = append(res.Written, c.Target)
	}

	// the manifest: what laib put on the box this time (#41)
	var managed []string
	for k := range p.Output {
		managed = append(managed, plan.Target(paths, k))
	}
	if err := plan.WriteManifest(paths, managed); err != nil {
		return res, fmt.Errorf("manifest: %w", err)
	}

	// The declared data paths exist before any unit starts: podman refuses a
	// bind mount of a missing directory (statfs: no such file), and a
	// directory podman created would belong to the wrong user.
	for _, name := range st.ServiceNames() {
		for _, d := range st.Services[name].Contract.Data {
			dir := strings.ReplaceAll(d.Path, "${DATA_DIR}", st.Box.DataDir)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return res, fmt.Errorf("data path %s: %w", dir, err)
			}
		}
	}

	// pre-start hooks: config files and folders the units need, before anything starts
	for _, name := range hookTargets(st, p) {
		if svc, ok := st.Services[name]; ok {
			if c, ok := runHook(ctx, st, svc, paths, "pre-start", secrets, res.NewSecrets); ok {
				res.Checks = append(res.Checks, c)
			}
		}
	}
	// a unit's first start has 90 s and must not include the image pull (#46):
	// the images of new or re-imaged containers are pulled now
	for _, image := range imagesToPull(p, previousImages) {
		if out, err := sysd.Run(ctx, "podman", "pull", "-q", image); err != nil {
			res.Checks = append(res.Checks, Check{Service: "laib", What: "pull " + image, OK: false, Detail: strings.TrimSpace(out)})
		}
	}
	if err := sysd.DaemonReload(ctx); err != nil {
		return res, fmt.Errorf("daemon-reload: %w", err)
	}
	for _, c := range p.Changes {
		if strings.HasPrefix(c.Path, "systemd/") && (strings.HasSuffix(c.Path, ".timer") || strings.HasSuffix(c.Path, ".service")) && !strings.Contains(c.Path, ".d/") && c.Kind != "delete" {
			unit := strings.TrimPrefix(c.Path, "systemd/")
			if !strings.Contains(p.Output[c.Path], "[Install]") {
				continue // a timer's oneshot service: the timer is enabled, the service only runs when it fires (VM run 23)
			}
			if _, err := sysd.Run(ctx, "systemctl", "--user", "enable", "--now", unit); err != nil {
				res.Checks = append(res.Checks, Check{Service: "laib", What: "enable " + unit, OK: false, Detail: err.Error()})
			} else {
				if strings.HasSuffix(unit, ".service") {
					sysd.Restart(ctx, unit) // a changed service unit must pick up its new environment
				}
				res.Restarted = append(res.Restarted, unit)
			}
		}
	}
	for _, c := range p.Changes {
		if c.Path == "nftables/laib.nft" && c.Kind != "delete" {
			// core has passwordless sudo on CoreOS; the ruleset is idempotent
			// (its own table), so loading it twice is harmless.
			if out, err := sysd.Run(ctx, "sudo", "nft", "-f", c.Target); err != nil {
				res.Checks = append(res.Checks, Check{Service: "laib", What: "nft -f", OK: false, Detail: out})
			} else {
				res.Restarted = append(res.Restarted, "nftables")
			}
		}
	}
	restart := map[string]bool{}
	for _, name := range p.Services {
		restart[name] = true
	}
	// A changed role output restarts the role's service.
	for _, c := range p.Changes {
		if c.Service == "" {
			switch strings.SplitN(c.Path, "/", 2)[0] {
			case "caddy":
				restart[st.Box.Roles.Proxy] = true
			case "authelia":
				restart[st.Box.Roles.SSO] = true
			case "adguard":
				restart[st.Box.Roles.DNS] = true
			}
		}
	}
	var restartOrder []string
	for name := range restart {
		if _, ok := st.Services[name]; ok {
			restartOrder = append(restartOrder, name)
		}
	}
	restartOrder = st.Order(restartOrder)
	for _, name := range restartOrder {
		for _, unit := range podRestartSet(st.Services[name].UnitNames()) {
			if err := sysd.Restart(ctx, unit); err != nil {
				res.Checks = append(res.Checks, Check{Service: name, What: "restart " + unit, OK: false, Detail: err.Error()})
				continue
			}
			res.Restarted = append(res.Restarted, unit)
		}
	}
	// Verify what changed — and every role restarted for a changed output
	// (#50: a Caddy restarted into a bad config was never looked at); with
	// no change, the scoped service or everything. A service the plan only
	// deleted (a rollback, a remove) is gone from the state: nothing to
	// verify, no hook to run (#41).
	var targets []string
	seenTarget := map[string]bool{}
	for _, name := range p.Services {
		if _, ok := st.Services[name]; ok && !seenTarget[name] {
			seenTarget[name] = true
			targets = append(targets, name)
		}
	}
	for _, name := range restartOrder {
		if _, ok := st.Services[name]; ok && !seenTarget[name] {
			seenTarget[name] = true
			targets = append(targets, name)
		}
	}
	if len(targets) == 0 {
		if p.Only != "" {
			targets = []string{p.Only}
		} else {
			targets = st.ServiceNames()
		}
	}
	res.Checks = append(res.Checks, Verify(ctx, st, st.Order(targets))...)
	// Post-deploy hooks run only for services whose verify is green.
	redByService := map[string]bool{}
	for _, c := range res.Checks {
		if !c.OK {
			redByService[c.Service] = true
		}
	}
	for _, name := range targets {
		if redByService[name] {
			continue
		}
		if c, ok := runHook(ctx, st, st.Services[name], paths, "post-deploy", secrets, res.NewSecrets); ok {
			res.Checks = append(res.Checks, c)
		}
	}
	res.Green = true
	for _, c := range res.Checks {
		if !c.OK {
			res.Green = false
		}
	}
	ClearPending(paths) // activated and verified
	return res, nil
}

// Verify checks the named services (all when nil): unit active, health answers.
func Verify(ctx context.Context, st *state.State, services []string) []Check {
	if services == nil {
		services = st.ServiceNames()
	}
	var checks []Check
	for _, name := range services {
		svc, ok := st.Services[name]
		if !ok {
			continue
		}
		for _, unit := range svc.UnitNames() {
			checks = append(checks, waitActive(ctx, name, unit))
		}
		if h := svc.Contract.Health.HTTP; h != "" {
			checks = append(checks, waitHTTP(ctx, name, h))
		}
	}
	return checks
}

func waitActive(ctx context.Context, service, unit string) Check {
	deadline := time.Now().Add(60 * time.Second)
	last := ""
	for time.Now().Before(deadline) {
		last = sysd.Active(ctx, unit)
		if last == "active" {
			return Check{Service: service, What: "unit " + unit, OK: true}
		}
		if last == "failed" {
			break
		}
		time.Sleep(2 * time.Second)
	}
	return Check{Service: service, What: "unit " + unit, OK: false, Detail: last}
}

func waitHTTP(ctx context.Context, service, url string) Check {
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	deadline := time.Now().Add(90 * time.Second)
	last := ""
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return Check{Service: service, What: "health " + url, OK: true, Detail: resp.Status}
			}
			last = resp.Status
		} else {
			last = err.Error()
		}
		time.Sleep(3 * time.Second)
	}
	return Check{Service: service, What: "health " + url, OK: false, Detail: last}
}

func writeAtomic(target, content string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	// Unit files carry rendered secrets (Environment=…); keep them private.
	mode := os.FileMode(0o644)
	if strings.Contains(content, "Environment=") || strings.Contains(content, "header_up X-Laib-Proxy") {
		mode = 0o600
	}
	tmp := target + ".laib-tmp"
	if err := os.WriteFile(tmp, []byte(content), mode); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// snapshot commits the state repo so the applied state has a hash. A clean
// tree returns HEAD.
func snapshot(ctx context.Context, repo, reason string) (string, error) {
	git := func(args ...string) (string, error) {
		return sysd.Run(ctx, "git", append([]string{"-C", repo}, args...)...)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		if _, err := git("init", "-q"); err != nil {
			return "", err
		}
	}
	if _, err := git("add", "-A"); err != nil {
		return "", err
	}
	status, _ := git("status", "--porcelain")
	if status != "" {
		msg := "apply: " + reason
		if reason == "" {
			msg = "apply"
		}
		if out, err := git("-c", "user.name=laib", "-c", "user.email=laib@localhost", "commit", "-q", "-m", msg); err != nil {
			return "", fmt.Errorf("%s: %w", out, err)
		}
	}
	return git("rev-parse", "--short", "HEAD")
}

// unitOfFile maps a rendered Quadlet or systemd file to its unit name.
func unitOfFile(path string) string {
	base := filepath.Base(path)
	switch {
	case strings.HasSuffix(base, ".container"):
		return strings.TrimSuffix(base, ".container") + ".service"
	case strings.HasSuffix(base, ".pod"):
		return strings.TrimSuffix(base, ".pod") + "-pod.service"
	case strings.HasPrefix(path, "systemd/") && (strings.HasSuffix(base, ".service") || strings.HasSuffix(base, ".timer")):
		return base
	}
	return ""
}

// ValidateOutput checks a rendered role output before it is written (#50).
// The Caddyfile runs through `caddy validate` in the proxy's own image —
// the config it will be fed, with the adapter it will use. Tests swap it.
var ValidateOutput = func(ctx context.Context, st *state.State, key, content string) error {
	if key != "caddy/Caddyfile" {
		return nil
	}
	image := "ghcr.io/mdopp/laib-caddy:2"
	if svc, ok := st.Services[st.Box.Roles.Proxy]; ok {
		if v, ok := svc.Values["IMAGE"]; ok && v.Literal != "" {
			image = v.Literal
		}
	}
	tmp, err := os.CreateTemp("", "laib-caddyfile-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	tmp.WriteString(content)
	tmp.Close()
	out, err := sysd.Run(ctx, "podman", "run", "--rm", "-v", tmp.Name()+":/tmp/Caddyfile:ro,z", image, "caddy", "validate", "--config", "/tmp/Caddyfile", "--adapter", "caddyfile")
	if err != nil {
		return fmt.Errorf("the rendered Caddyfile does not validate (nothing written): %s", strings.TrimSpace(out))
	}
	return nil
}

// imageOf returns the Image= of a container unit.
func imageOf(unit string) string {
	for _, line := range strings.Split(unit, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Image=") {
			return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "Image="))
		}
	}
	return ""
}

// imagesToPull: the images of added containers and of containers whose
// Image= changed, each once.
func imagesToPull(p *plan.Plan, previous map[string]string) []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range p.Changes {
		if c.Kind == "delete" || !strings.HasSuffix(c.Path, ".container") {
			continue
		}
		img := imageOf(p.Output[c.Path])
		if img == "" || strings.Contains(img, "${") || seen[img] || (c.Kind == "change" && previous[c.Path] == img) {
			continue
		}
		seen[img] = true
		out = append(out, img)
	}
	return out
}

// DefaultHooksImage is where hooks run when box.yml names none.
const DefaultHooksImage = "ghcr.io/mdopp/laib-hooks:latest"

// HookSpec is one hook run: what to mount, the environment file, the command.
type HookSpec struct {
	Image   string
	Mounts  []string // host paths mounted at the same path inside; suffix ":ro" for read-only
	EnvFile string   // KEY=value lines, mode 0600, removed after the run
	Dir     string   // working directory (the service folder)
	Command []string // the hook, by its path inside the container (same as on the host)
}

// RunHook executes a hook spec and returns its combined output. The default
// runs it in the hooks image with podman (host network, so the hook reaches
// the service on 127.0.0.1 and the service's data paths at their host
// paths). Swapped in tests.
var RunHook = func(ctx context.Context, h HookSpec) (string, error) {
	args := []string{"run", "--rm", "--network", "host", "--env-file", h.EnvFile, "-w", h.Dir}
	for _, m := range h.Mounts {
		src, opt := m, "z"
		if strings.HasSuffix(m, ":ro") {
			src, opt = strings.TrimSuffix(m, ":ro"), "ro,z"
		}
		args = append(args, "-v", src+":"+src+":"+opt)
	}
	args = append(args, h.Image)
	args = append(args, h.Command...)
	return sysd.Run(ctx, "podman", args...)
}

// hookPhases are the two moments a template may act (ADR 0004 amended, #36):
// pre-start renders config and folders before the units start and may not
// assume the service is up; post-deploy talks to the running service.
var hookPhases = []string{"pre-start", "post-deploy"}

// runHook runs services/<name>/<phase> if it exists and is executable, in
// the hooks image, never on the host. The hook gets the builtins, every
// value and LAIB_SECRET_<NAME> for the service's declared secrets in its
// environment, the service folder read-only, the render folder read-only
// and the service's data paths read-write at their host paths. It may
// print `laib-secret: NAME=value` to hand a credential back into
// secrets.age, and `laib-exec: <container> <cmd…>` to have Laib run a
// command in one of the service's own containers (the one thing a hook
// cannot do from inside its image). Non-zero exit is red.
func runHook(ctx context.Context, st *state.State, svc *state.Service, paths boxfs.Paths, phase string, secrets map[string]string, newSecrets map[string]string) (Check, bool) {
	hook := filepath.Join(svc.Dir, phase)
	info, err := os.Stat(hook)
	if err != nil || info.IsDir() {
		return Check{}, false
	}
	if info.Mode()&0o111 == 0 {
		// present but not executable: a red check, never a silent skip (PR #70's first VM run)
		return Check{Service: svc.Name, What: phase, OK: false, Detail: "hook is not executable (chmod +x services/" + svc.Name + "/" + phase + ")"}, true
	}
	modelKey := ""
	if st.Box.Model.KeySecret != "" {
		modelKey = secrets[st.Box.Model.KeySecret]
	}
	var env []string
	env = append(env,
		"LAIB_SERVICE="+svc.Name, "LAIB_DOMAIN="+st.Box.Domain, "LAIB_DATA_DIR="+st.Box.DataDir,
		"LAIB_TZ="+st.Box.Timezone, "LAIB_LAN_IP="+st.Box.LanIP, "LAIB_PHASE="+phase, "LAIB_RENDER_DIR="+paths.Render,
		"MODEL_URL="+st.ModelURL(), "MODEL_LEASE="+st.ModelLease(), "MODEL_KEY="+modelKey,
		"LAIB_MCP_URL=http://host.containers.internal:"+fmt.Sprint(st.Box.Laib.Port)+"/mcp")
	for k, v := range svc.Values {
		val := v.Literal
		if v.Secret != "" {
			val = secrets[v.Secret]
		}
		env = append(env, k+"="+val)
	}
	for _, sec := range svc.Contract.Secrets {
		env = append(env, "LAIB_SECRET_"+sec.Name+"="+secrets[sec.Name])
	}
	envFile, err := os.CreateTemp(paths.Home, ".hook-env-*")
	if err != nil {
		return Check{Service: svc.Name, What: phase, OK: false, Detail: err.Error()}, true
	}
	defer os.Remove(envFile.Name())
	os.Chmod(envFile.Name(), 0o600)
	envFile.WriteString(strings.Join(env, "\n") + "\n")
	envFile.Close()
	image := st.Box.Laib.HooksImage
	if image == "" {
		image = DefaultHooksImage
	}
	mounts := []string{svc.Dir + ":ro", paths.Render + ":ro"}
	for _, d := range svc.Contract.Data {
		p := strings.ReplaceAll(d.Path, "${DATA_DIR}", st.Box.DataDir)
		os.MkdirAll(p, 0o755)
		mounts = append(mounts, p)
	}
	hctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	out, err := RunHook(hctx, HookSpec{Image: image, Mounts: mounts, EnvFile: envFile.Name(), Dir: svc.Dir, Command: []string{hook}})
	var kept []string
	own := map[string]bool{}
	for _, u := range svc.UnitNames() {
		if strings.HasSuffix(u, ".service") && !strings.HasSuffix(u, "-pod.service") {
			own["systemd-"+strings.TrimSuffix(u, ".service")] = true
		}
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "laib-secret: "):
			if k, v, ok := strings.Cut(strings.TrimPrefix(line, "laib-secret: "), "="); ok && k != "" {
				newSecrets[k] = v
			}
		case strings.HasPrefix(line, "laib-exec: "):
			f := strings.Fields(strings.TrimPrefix(line, "laib-exec: "))
			if len(f) < 2 || !own[f[0]] {
				kept = append(kept, "laib-exec refused (not one of "+svc.Name+"'s containers): "+line)
				err = errors.Join(err, fmt.Errorf("laib-exec refused: %s", line))
				continue
			}
			o, e := sysd.Run(hctx, "podman", append([]string{"exec", f[0]}, f[1:]...)...)
			kept = append(kept, "laib-exec "+f[0]+" "+f[1]+": "+tailOf(o, 200))
			if e != nil {
				err = errors.Join(err, fmt.Errorf("laib-exec %s: %w", f[1], e))
			}
		default:
			kept = append(kept, line)
		}
	}
	tail := strings.TrimSpace(strings.Join(kept, "\n"))
	if len(tail) > 2000 {
		tail = "…" + tail[len(tail)-2000:]
	}
	if err != nil {
		return Check{Service: svc.Name, What: phase, OK: false, Detail: err.Error() + "\n" + tail}, true
	}
	return Check{Service: svc.Name, What: phase, OK: true, Detail: tail}, true
}

func tailOf(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}

// podRestartSet: a service with a Quadlet pod restarts the pod unit only.
// Its containers are BindsTo= the pod and Wants= by it, so they stop and
// start with it; restarting them one by one on top is not harmless — the
// pod runs with --exit-policy stop, and a container restart that is the
// last one running stops the pod under the others (#70's VM runs: lldap's
// restart stopped the auth pod, Authelia then failed its start limit).
func podRestartSet(units []string) []string {
	var pods, rest []string
	for _, u := range units {
		if strings.HasSuffix(u, "-pod.service") {
			pods = append(pods, u)
		} else {
			rest = append(rest, u)
		}
	}
	if len(pods) > 0 {
		return pods
	}
	return rest
}

// hookTargets: the services whose hooks run for this plan — those with a
// change of their own, plus the role holders whose rendered files changed
// (authelia/*.yml → sso, caddy/Caddyfile → proxy, adguard/rewrites.json →
// dns): their config is written by their hook from those files.
func hookTargets(st *state.State, p *plan.Plan) []string {
	out := append([]string{}, p.Services...)
	seen := map[string]bool{}
	for _, n := range out {
		seen[n] = true
	}
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	for _, c := range p.Changes {
		switch strings.SplitN(c.Path, "/", 2)[0] {
		case "authelia":
			add(st.Box.Roles.SSO)
		case "caddy":
			add(st.Box.Roles.Proxy)
		case "adguard":
			add(st.Box.Roles.DNS)
		}
	}
	return out
}
