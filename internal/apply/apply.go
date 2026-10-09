// Package apply makes the box match the state: snapshot, write, reload,
// restart, verify. A red verify is a red result, never a retry.
package apply

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
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

// ErrLocked means another apply holds the lock (state `eingeschränkt`).
var ErrLocked = errors.New("apply is locked")

// Lock takes the apply lock; the content names the holder for /ready.
func Lock(paths boxfs.Paths, holder string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(paths.Lock), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(paths.Lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, ErrLocked
		}
		return nil, err
	}
	fmt.Fprint(f, holder)
	f.Close()
	return func() { os.Remove(paths.Lock) }, nil
}

// LockHolder returns who holds the lock, or "".
func LockHolder(paths boxfs.Paths) string {
	b, err := os.ReadFile(paths.Lock)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Run applies a computed plan. The snapshot is a Git commit of the state repo
// (the data-volume snapshot comes with backup, later); every written file is
// written atomically.
func Run(ctx context.Context, st *state.State, p *plan.Plan, paths boxfs.Paths, secrets map[string]string, reason string) (Result, error) {
	res := Result{NewSecrets: map[string]string{}}
	commit, err := snapshot(ctx, paths.State, reason)
	if err != nil {
		return res, fmt.Errorf("snapshot: %w", err)
	}
	res.Commit = commit

	for _, c := range p.Changes {
		if c.Kind == "delete" {
			if err := os.Remove(c.Target); err != nil && !errors.Is(err, os.ErrNotExist) {
				return res, err
			}
			res.Written = append(res.Written, c.Target+" (removed)")
			continue
		}
		if err := writeAtomic(c.Target, p.Output[c.Path]); err != nil {
			return res, err
		}
		res.Written = append(res.Written, c.Target)
	}

	if err := sysd.DaemonReload(ctx); err != nil {
		return res, fmt.Errorf("daemon-reload: %w", err)
	}
	for _, c := range p.Changes {
		if strings.HasPrefix(c.Path, "systemd/") && strings.HasSuffix(c.Path, ".timer") && c.Kind != "delete" {
			unit := strings.TrimPrefix(c.Path, "systemd/")
			if _, err := sysd.Run(ctx, "systemctl", "--user", "enable", "--now", unit); err != nil {
				res.Checks = append(res.Checks, Check{Service: "laib", What: "enable " + unit, OK: false, Detail: err.Error()})
			} else {
				res.Restarted = append(res.Restarted, unit)
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
	for _, name := range st.ServiceNames() {
		if !restart[name] {
			continue
		}
		for _, unit := range st.Services[name].UnitNames() {
			if err := sysd.Restart(ctx, unit); err != nil {
				res.Checks = append(res.Checks, Check{Service: name, What: "restart " + unit, OK: false, Detail: err.Error()})
				continue
			}
			res.Restarted = append(res.Restarted, unit)
		}
	}
	res.Checks = append(res.Checks, Verify(ctx, st, p.Services)...)
	// Post-deploy hooks run only for services whose verify is green.
	redByService := map[string]bool{}
	for _, c := range res.Checks {
		if !c.OK {
			redByService[c.Service] = true
		}
	}
	for _, name := range p.Services {
		if redByService[name] {
			continue
		}
		if c, ok := postDeploy(ctx, st, st.Services[name], secrets, res.NewSecrets); ok {
			res.Checks = append(res.Checks, c)
		}
	}
	res.Green = true
	for _, c := range res.Checks {
		if !c.OK {
			res.Green = false
		}
	}
	return res, nil
}

// postDeploy runs services/<name>/post-deploy if it exists and is executable
// (ADR 0004). It gets the builtins, every value and LAIB_SECRET_<NAME> for the
// service's declared secrets in its environment, runs in the service folder,
// and may print `laib-secret: NAME=value` lines to hand a credential it
// created (an API token, say) back into secrets.age. Non-zero exit is red.
func postDeploy(ctx context.Context, st *state.State, svc *state.Service, secrets map[string]string, newSecrets map[string]string) (Check, bool) {
	hook := filepath.Join(svc.Dir, "post-deploy")
	info, err := os.Stat(hook)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return Check{}, false
	}
	env := append(os.Environ(),
		"LAIB_SERVICE="+svc.Name, "LAIB_DOMAIN="+st.Box.Domain, "LAIB_DATA_DIR="+st.Box.DataDir,
		"LAIB_TZ="+st.Box.Timezone, "LAIB_LAN_IP="+st.Box.LanIP)
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
	hctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(hctx, hook)
	cmd.Dir = svc.Dir
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	var kept []string
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "laib-secret: ") {
			if k, v, ok := strings.Cut(strings.TrimPrefix(line, "laib-secret: "), "="); ok && k != "" {
				newSecrets[k] = v
			}
			continue
		}
		kept = append(kept, line)
	}
	tail := strings.TrimSpace(strings.Join(kept, "\n"))
	if len(tail) > 2000 {
		tail = "…" + tail[len(tail)-2000:]
	}
	for k, v := range secrets {
		if v != "" {
			tail = strings.ReplaceAll(tail, v, "${secret:"+k+"}")
		}
	}
	if err != nil {
		return Check{Service: svc.Name, What: "post-deploy", OK: false, Detail: err.Error() + "\n" + tail}, true
	}
	return Check{Service: svc.Name, What: "post-deploy", OK: true, Detail: tail}, true
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
	tmp := target + ".laib-tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
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
