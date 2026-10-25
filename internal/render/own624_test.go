package render

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// mountsOf lists a unit's Volume= and Mount= values and fails on what lets a
// container reach past them: an env file, podman args, capabilities, an
// unconfined label, or a mount or line naming the age key, secrets.age, the
// state repo or the journal.
func mountsOf(t *testing.T, name, unit string) []string {
	t.Helper()
	home := LaibHome
	never := []string{filepath.Join(home, "age.key"), filepath.Join(home, "state", "secrets.age"), filepath.Join(home, "state"), filepath.Join(home, "journal")}
	var mounts []string
	for _, line := range strings.Split(unit, "\n") {
		k, v, _ := strings.Cut(line, "=")
		switch k {
		case "Volume", "Mount":
			mounts = append(mounts, v)
			src := strings.Split(v, ":")[0]
			for _, p := range never {
				if p == src || strings.HasPrefix(p, strings.TrimSuffix(src, "/")+"/") {
					t.Errorf("%s mounts %s, which holds %s", name, src, p)
				}
			}
		case "EnvironmentFile", "PodmanArgs", "AddCapability", "SecurityLabelDisable", "Rootfs", "UserNS":
			t.Errorf("%s carries %s", name, line)
		case "Environment":
			for _, p := range never {
				if strings.Contains(v, p) {
					t.Errorf("%s names %s in its environment: %s", name, p, line)
				}
			}
		}
	}
	for _, w := range []string{"\nNetwork=host\n", "\nReadOnly=true\n", "\nNoNewPrivileges=true\n", "\nDropCapability=all\n"} {
		if !strings.Contains(unit, w) {
			t.Errorf("%s lacks %q:\n%s", name, strings.TrimSpace(w), unit)
		}
	}
	return mounts
}

// ADR 0027 (#624, part 2): laib-haushalt is a long-running container on
// every box, not a program the door runs as core. It listens on 127.0.0.1
// in the host's network (net geraete reads the neighbour table), calls laib
// back with its own propose-only token, and the door's key is how it knows
// a call came from laib. Its only mount is the scan service's orders
// folder, at the same path, and only while scan is there.
func TestHaushaltRunsInAContainerThatSeesOnlyTheScanOrders(t *testing.T) {
	st, err := state.Load("../../testdata/golden/nuc/state")
	if err != nil {
		t.Fatal(err)
	}
	sec := map[string]string{HaushaltToken: "hh-token-1", HaushaltKey: "hh-key-2"}
	out, err := Render(st, sec)
	if err != nil {
		t.Fatal(err)
	}
	unit := out["quadlet/laib-haushalt/laib-haushalt.container"]
	if unit == "" {
		t.Fatal("no laib-haushalt container rendered")
	}
	for _, w := range []string{"\nImage=" + HaushaltImage + "\n", `Environment="HAUSHALT_LISTEN=127.0.0.1:5902"`, `Environment="LAIB_MCP_URL=http://127.0.0.1:5900/mcp"`,
		"\nSecret=laib.laib-haushalt.laib-haushalt.LAIB_TOKEN,type=env,target=LAIB_TOKEN\n", "\nSecret=laib.laib-haushalt.laib-haushalt.HAUSHALT_KEY,type=env,target=HAUSHALT_KEY\n",
		"Restart=always\n", "WantedBy=default.target\n"} {
		if !strings.Contains(unit, w) {
			t.Errorf("laib-haushalt lacks %q:\n%s", strings.TrimSpace(w), unit)
		}
	}
	if m := mountsOf(t, "laib-haushalt", unit); len(m) != 0 {
		t.Errorf("without the scan service laib-haushalt mounts nothing, got %v", m)
	}
	for _, v := range sec {
		if strings.Contains(unit, v) {
			t.Error("a secret value in laib-haushalt's unit")
		}
	}
	pod := PodmanSecrets(st, sec)
	if pod["laib.laib-haushalt.laib-haushalt.LAIB_TOKEN"] != "hh-token-1" || pod["laib.laib-haushalt.laib-haushalt.HAUSHALT_KEY"] != "hh-key-2" {
		t.Errorf("the token and the door's key are no podman secrets: %v", pod)
	}
	names := SecretNames(st)
	if !slices.ContainsFunc(names, func(s state.Secret) bool { return s.Name == HaushaltToken && s.External && s.Mint == "haushalt" }) ||
		!slices.ContainsFunc(names, func(s state.Secret) bool { return s.Name == HaushaltKey && !s.External }) {
		t.Errorf("the token (minted for haushalt) and the key (generated) are not declared: %+v", names)
	}

	// with scan: the orders folder beside its status file, the same path inside, shared with the scan container
	st.Services["scan"] = &state.Service{Name: "scan", Values: map[string]state.Value{}, Units: map[string]string{"scan.container": "[Container]\nImage=x\n"},
		Contract: state.Contract{Provides: []string{"scan"}, Status: &state.StatusFile{File: "${DATA_DIR}/scan/laib-status.json"}}}
	out, err = Render(st, sec)
	if err != nil {
		t.Fatal(err)
	}
	unit = out["quadlet/laib-haushalt/laib-haushalt.container"]
	orders := filepath.Join(st.Box.DataDir, "scan", "auftrag")
	if m := mountsOf(t, "laib-haushalt", unit); !slices.Equal(m, []string{orders + ":" + orders + ":z"}) {
		t.Errorf("with scan laib-haushalt mounts its orders folder only, got %v", m)
	}
	if !strings.Contains(unit, "\nExecStartPre=/usr/bin/mkdir -p "+orders+"\n") {
		t.Errorf("the orders folder is not made before the start:\n%s", unit)
	}
	if svc, status, o := ScanFolder(st); svc != "scan" || status != filepath.Join(st.Box.DataDir, "scan", "laib-status.json") || o != orders {
		t.Errorf("ScanFolder: %q %q %q", svc, status, o)
	}
}

// ADR 0027 (#624, part 2): the llama template runs laib-lease as a
// container, not as a host unit: the image the release pins (the template
// names the floating tag, laib puts its own digest in), the lease folder
// and presets.json read-only as its only mounts, the model key and its
// laib token as podman secrets, and laib's MCP on the host's loopback.
func TestTheLeaseRunsInAContainerFromTheImageLaibPins(t *testing.T) {
	dir := filepath.Join("..", "..", "templates", "llama")
	if _, err := os.Stat(filepath.Join(dir, "llama-lease.service")); err == nil {
		t.Fatal("the llama template still ships laib-lease as a host unit")
	}
	tpl, err := os.ReadFile(filepath.Join(dir, "llama-lease.container"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(tpl), "\nImage="+LeaseLatest+"\n") {
		t.Fatalf("the template's lease does not name %s:\n%s", LeaseLatest, tpl)
	}
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "box.yml"), []byte("domain: x.home\ndataDir: /mnt/data/stacks\nlanIp: 10.0.0.2\ngpu: {present: true}\nhostNetwork: [llama]\nroles: {model: llama, sso: llama}\n"), 0o644)
	os.WriteFile(filepath.Join(tmp, "policy.yml"), []byte("tiers: [read, propose, apply, destroy]\nhands: {human: {ceiling: destroy}}\n"), 0o644)
	dst := filepath.Join(tmp, "services", "llama")
	os.MkdirAll(dst, 0o755)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() {
			b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
			os.WriteFile(filepath.Join(dst, e.Name()), b, 0o644)
		}
	}
	b, _ := os.ReadFile(filepath.Join(dir, "values.example.yml"))
	os.WriteFile(filepath.Join(dst, "values.yml"), b, 0o644)
	st, err := state.Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	old := LeaseImage
	LeaseImage = "ghcr.io/mdopp/laib-lease@sha256:1234"
	t.Cleanup(func() { LeaseImage = old })
	sec := map[string]string{"MODEL_API_KEY": "model-key-1", "LLAMA_LAIB_TOKEN": "laib_lease_2"}
	out, err := Render(st, sec)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["systemd/llama-lease.service"]; ok {
		t.Fatal("laib-lease is still rendered as a host unit")
	}
	unit := out["quadlet/llama/llama-lease.container"]
	if !strings.Contains(unit, "\nImage=ghcr.io/mdopp/laib-lease@sha256:1234\n") {
		t.Fatalf("the lease does not run the image laib pins:\n%s", unit)
	}
	want := []string{"/mnt/data/stacks/llama/lease:/lease:z", "%h/.config/containers/systemd/llama/presets.json:/presets.json:ro"}
	if m := mountsOf(t, "llama-lease", unit); !slices.Equal(m, want) {
		t.Errorf("the lease mounts %v, want its folder and presets.json read-only", m)
	}
	for _, w := range []string{"Environment=LEASE_DIR=/lease", "Environment=PRESETS_FILE=/presets.json", "Environment=LAIB_MCP_URL=http://127.0.0.1:5900/mcp",
		"Secret=laib.llama.llama-lease.LEASE_TOKEN,type=env,target=LEASE_TOKEN", "Secret=laib.llama.llama-lease.LAIB_TOKEN,type=env,target=LAIB_TOKEN"} {
		if !strings.Contains(unit, w+"\n") {
			t.Errorf("the lease lacks %q:\n%s", w, unit)
		}
	}
	for _, v := range sec {
		if strings.Contains(unit, v) {
			t.Error("a secret value in the lease's unit")
		}
	}
	if !slices.ContainsFunc(st.Services["llama"].Contract.Secrets, func(s state.Secret) bool { return s.Name == "LLAMA_LAIB_TOKEN" && s.External && s.Mint == "lease" }) {
		t.Errorf("the lease's laib token is not minted for the lease hand: %+v", st.Services["llama"].Contract.Secrets)
	}
}
