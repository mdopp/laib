// Package haushalt is the program laib-haushalt (ADR 0026, #598): the
// household verbs that read the state, leave an order for a service or file
// a proposal — scan and net geraete. The kernel serves them through its verb
// door: it checks hand, tier and verb, then runs this program with the
// actor, the state repo and laib itself to call back for a proposal. It
// imports nothing of the kernel, and changes nothing of the box itself.
package haushalt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Exit codes, as the kernel's (docs/INTERFACE.md).
const (
	exitOK      = 0
	exitRefused = 3
)

// Env is what the door hands over (cmd/laib/door.go).
type Env struct {
	Actor  string // LAIB_ACTOR
	State  string // LAIB_STATE: the state repo, read only here
	Kernel string // LAIB_KERNEL: laib, for a proposal
	JSON   bool
	In     io.Reader
	Out    io.Writer
	Err    io.Writer
	// Journal is fd 3: the kernel writes each line as the caller; nil, nothing
	Journal io.Writer
}

// Run is one verb: scan … or net geraete ….
func Run(ctx context.Context, env *Env, args []string) int {
	switch {
	case len(args) > 0 && args[0] == "scan":
		return Scan(ctx, env, args[1:])
	case len(args) > 1 && args[0] == "net" && args[1] == "geraete":
		return NetDevices(env, args[2:])
	}
	return env.fail(errors.New("usage: laib-haushalt [--json] scan … | net geraete … (the kernel serves them as laib scan, laib net geraete)"))
}

func (e *Env) fail(err error) int {
	fmt.Fprintln(e.Err, "laib:", strings.TrimSpace(err.Error()))
	return 1
}

func (e *Env) refused(msg string) int {
	fmt.Fprintln(e.Err, "laib:", msg)
	return exitRefused
}

func (e *Env) emit(v any) int {
	enc := json.NewEncoder(e.Out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return e.fail(err)
	}
	return exitOK
}

// journal hands one line to the kernel; it sets actor and verb itself.
func (e *Env) journal(service, reason, detail string) {
	if e.Journal != nil {
		b, _ := json.Marshal(map[string]string{"service": service, "reason": reason, "result": "info", "detail": detail})
		e.Journal.Write(append(b, '\n'))
	}
}

// propose is `laib propose … --json` as the actor: the kernel's review,
// class and Inbox decide it; behind the door the dial applies nothing.
func (e *Env) propose(ctx context.Context, args ...string) (id int, class string, code int) {
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, e.Kernel, append([]string{"propose", "--json"}, args...)...)
	cmd.Stdout, cmd.Stderr = &out, e.Err
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return 0, "", exit.ExitCode()
	}
	var p struct {
		ID    int    `json:"id"`
		Class string `json:"class"`
	}
	if err != nil || json.Unmarshal(out.Bytes(), &p) != nil || p.ID == 0 {
		return 0, "", e.fail(fmt.Errorf("laib propose: %v: %s", err, out.String()))
	}
	return p.ID, p.Class, exitOK
}

// box is what this program reads of box.yml.
type box struct {
	DataDir string `yaml:"dataDir"`
	LanIP   string `yaml:"lanIp"`
}

func loadBox(state string) (box, error) {
	var b box
	raw, err := os.ReadFile(filepath.Join(state, "box.yml"))
	if err == nil {
		err = yaml.Unmarshal(raw, &b)
	}
	return b, err
}

// service is what this program reads of a service's laib.yml.
type service struct {
	Name     string
	Provides []string `yaml:"provides"`
	Status   *struct {
		File string `yaml:"file"` // under ${DATA_DIR}
	} `yaml:"status"`
}

// provider is the installed service that provides purpose, or nil.
func provider(state, purpose string) (*service, error) {
	dirs, _ := filepath.Glob(filepath.Join(state, "services", "*", "laib.yml"))
	var found *service
	for _, f := range dirs {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		s := &service{Name: filepath.Base(filepath.Dir(f))}
		if err := yaml.Unmarshal(raw, s); err != nil {
			return nil, fmt.Errorf("services/%s/laib.yml: %w", s.Name, err)
		}
		if slices.Contains(s.Provides, purpose) {
			found = s
		}
	}
	return found, nil
}

// writeAtomic is boxfs.WriteAtomic's shape (#148), which this program may
// not import: the temp file synced before the rename, the directory after.
func writeAtomic(tmp, path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
