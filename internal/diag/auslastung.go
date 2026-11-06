package diag

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/state"
)

// Load is `laib auslastung` (read, owner 2026-11-06): the box's values now,
// for Homes' Box → Auslastung. Read from /proc, /sys, statfs, nvidia-smi on
// the host and the lease's open GET; kept loadWait, so a page that asks
// every few seconds samples the CPU once per window. The RAID's state is
// diag's (probe raid), not repeated here; Missing says what was not readable.
type Load struct {
	At      time.Time `json:"at"`
	Uptime  int64     `json:"uptime_s"`
	CPU     CPULoad   `json:"cpu"`
	Mem     Used      `json:"mem"`
	Swap    Used      `json:"swap"`
	Disks   []Disk    `json:"disks"`
	RAID    string    `json:"raid"`
	GPU     *GPULoad  `json:"gpu,omitempty"`
	Temps   []Temp    `json:"temps"`
	Missing []string  `json:"missing,omitempty"`
}

type CPULoad struct {
	Cores int        `json:"cores"`
	Load  [3]float64 `json:"load"` // 1, 5, 15 minutes
	Pct   float64    `json:"pct"`  // busy over about a second
}

type Used struct {
	Total uint64 `json:"total"` // bytes
	Used  uint64 `json:"used"`
}

// Disk is one filesystem by its role: system (/var: on CoreOS / is the
// immutable composefs), data (box.yml dataDir), boot.
type Disk struct {
	Role string  `json:"role"`
	Path string  `json:"path"`
	Size uint64  `json:"size"`
	Used uint64  `json:"used"`
	Free uint64  `json:"free"`
	Pct  float64 `json:"pct"`
}

type GPULoad struct {
	Name       string  `json:"name"`
	Pct        int     `json:"pct"`
	MemUsed    uint64  `json:"memUsed"` // bytes
	MemTotal   uint64  `json:"memTotal"`
	Temp       int     `json:"temp"`
	Power      float64 `json:"power"` // W
	PowerLimit float64 `json:"powerLimit"`
	// Holder holds the card through the lease; "" is the household window, Window its name
	Holder string `json:"holder"`
	Window string `json:"window"`
	Model  string `json:"model"`
}

type Temp struct {
	Name string  `json:"name"` // cpu, nvme0, sda
	C    float64 `json:"c"`
}

var (
	procRoot = "/proc"
	sysRoot  = "/sys"
	smi      = "nvidia-smi"
	loadWait = 4 * time.Second
	loadMu   sync.Mutex
	loadLast Load
)

// LoadCmd is the verb: always JSON.
func LoadCmd(ctx context.Context, env *cli.Env, _ []string) int {
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	loadMu.Lock()
	defer loadMu.Unlock()
	if time.Since(loadLast.At) > loadWait {
		loadLast = measure(ctx, st)
	}
	return cli.Emit(env, loadLast)
}

func measure(ctx context.Context, st *state.State) Load {
	l := Load{At: time.Now(), RAID: "laib diag, Probe raid", Temps: []Temp{}}
	var gpuErr string
	var wg sync.WaitGroup
	if st.Box.GPU.Present {
		wg.Add(1)
		go func() { defer wg.Done(); l.GPU, gpuErr = gpuLoad(ctx, st.ModelLease()) }()
	}
	before := cpuTicks()
	time.Sleep(time.Second)
	if now := cpuTicks(); now[1] > before[1] {
		l.CPU.Pct = round1(100 * (now[0] - before[0]) / (now[1] - before[1]))
	}
	l.CPU.Cores = runtime.NumCPU()
	if f := fields(procRoot, "loadavg"); len(f) >= 3 {
		for i := range 3 {
			l.CPU.Load[i], _ = strconv.ParseFloat(f[i], 64)
		}
	}
	if f := fields(procRoot, "uptime"); len(f) > 0 {
		u, _ := strconv.ParseFloat(f[0], 64)
		l.Uptime = int64(u)
	}
	m := meminfo()
	l.Mem = Used{m["MemTotal"], m["MemTotal"] - m["MemAvailable"]}
	l.Swap = Used{m["SwapTotal"], m["SwapTotal"] - m["SwapFree"]}
	seen := map[[2]int32]bool{}
	for _, d := range [][2]string{{"system", "/var"}, {"data", st.Box.DataDir}, {"boot", "/boot"}} {
		var fs syscall.Statfs_t
		if d[1] == "" || syscall.Statfs(d[1], &fs) != nil || fs.Blocks == 0 || seen[fs.Fsid.X__val] {
			continue
		}
		seen[fs.Fsid.X__val] = true
		bs := uint64(fs.Bsize)
		used, free := (fs.Blocks-fs.Bfree)*bs, fs.Bavail*bs
		l.Disks = append(l.Disks, Disk{d[0], d[1], fs.Blocks * bs, used, free, round1(100 * float64(used) / float64(used+free))})
	}
	l.Temps = append(l.Temps, hwmonTemps(sysRoot)...)
	wg.Wait()
	if gpuErr != "" {
		l.Missing = append(l.Missing, gpuErr)
	}
	if !hasTemp(l.Temps, "cpu") {
		l.Missing = append(l.Missing, "CPU-Temperatur: kein Sensor lesbar (coretemp, k10temp)")
	}
	return l
}

// cpuTicks is /proc/stat's first line: busy and all ticks.
func cpuTicks() [2]float64 {
	f := fields(procRoot, "stat")
	var busy, total float64
	for i := 1; i < len(f) && i <= 8; i++ {
		v, _ := strconv.ParseFloat(f[i], 64)
		total += v
		if i != 4 && i != 5 { // idle, iowait
			busy += v
		}
	}
	return [2]float64{busy, total}
}

func fields(root, name string) []string {
	b, _ := os.ReadFile(filepath.Join(root, name))
	line, _, _ := strings.Cut(string(b), "\n")
	return strings.Fields(line)
}

// meminfo is /proc/meminfo in bytes.
func meminfo() map[string]uint64 {
	out := map[string]uint64{}
	f, err := os.Open(filepath.Join(procRoot, "meminfo"))
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), ":")
		n, _ := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(v), " kB"), 10, 64)
		out[k] = n << 10
	}
	return out
}

// hwmonTemps: the CPU package (coretemp's "Package id 0", k10temp's Tctl),
// each NVMe's composite and a SATA disk's where drivetemp is loaded —
// without smartctl, which the host does not have.
func hwmonTemps(sys string) []Temp {
	var out []Temp
	dirs, _ := filepath.Glob(filepath.Join(sys, "class/hwmon/hwmon*"))
	for _, d := range dirs {
		name := readTrim(filepath.Join(d, "name"))
		input := filepath.Join(d, "temp1_input")
		switch name {
		case "coretemp", "k10temp":
			for _, lab := range []string{"Package id 0", "Tctl", "Tdie"} {
				if p := labelled(d, lab); p != "" {
					input = p
					break
				}
			}
			name = "cpu"
		case "nvme":
			name = filepath.Base(readLink(filepath.Join(d, "device")))
		case "drivetemp":
			blocks, _ := filepath.Glob(filepath.Join(d, "device", "block", "*"))
			if len(blocks) == 0 {
				continue
			}
			name = filepath.Base(blocks[0])
		default:
			continue
		}
		if v, err := strconv.Atoi(readTrim(input)); err == nil && !hasTemp(out, name) {
			out = append(out, Temp{name, float64(v) / 1000})
		}
	}
	return out
}

func labelled(dir, label string) string {
	ls, _ := filepath.Glob(filepath.Join(dir, "temp*_label"))
	for _, l := range ls {
		if readTrim(l) == label {
			return strings.TrimSuffix(l, "_label") + "_input"
		}
	}
	return ""
}

func readTrim(p string) string { b, _ := os.ReadFile(p); return strings.TrimSpace(string(b)) }

func readLink(p string) string { s, _ := filepath.EvalSymlinks(p); return s }

func hasTemp(ts []Temp, name string) bool {
	for _, t := range ts {
		if t.Name == name {
			return true
		}
	}
	return false
}

// gpuLoad asks nvidia-smi (the host's, from the uCore NVIDIA image) about
// the first card and the lease who holds it.
func gpuLoad(ctx context.Context, lease string) (*GPULoad, string) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, smi, "--query-gpu=name,utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw,power.limit", "--format=csv,noheader,nounits").Output()
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	f := strings.Split(line, ",")
	if err != nil || len(f) < 7 {
		return nil, fmt.Sprintf("Grafikkarte: nvidia-smi antwortet nicht (%v)", err)
	}
	num := func(i int) float64 { v, _ := strconv.ParseFloat(strings.TrimSpace(f[i]), 64); return v }
	g := &GPULoad{Name: strings.TrimSpace(f[0]), Pct: int(num(1)), MemUsed: uint64(num(2)) << 20, MemTotal: uint64(num(3)) << 20, Temp: int(num(4)), Power: num(5), PowerLimit: num(6)}
	if lease == "" {
		return g, ""
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, lease, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return g, "Grafikkarte: die Lease antwortet nicht, wer sie hält ist unbekannt"
	}
	defer resp.Body.Close()
	var l struct{ Holder, Mode, Alias string }
	json.NewDecoder(resp.Body).Decode(&l)
	g.Holder, g.Window, g.Model = l.Holder, l.Mode, l.Alias
	return g, ""
}

func round1(v float64) float64 { return float64(int(v*10+0.5)) / 10 }
