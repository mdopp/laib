package render

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// #195: the memory library (haus#13) is qmd in the haus pod — loopback only,
// CPU only, its embed model pinned in index.yml, refreshed by a timer whose
// oneshot fails when a step fails and never runs `qmd pull`.
func TestHausRunsTheLibraryOnItsLoopback(t *testing.T) {
	st := shippedBox(t)
	out, err := Render(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	has := func(file string, want ...string) string {
		t.Helper()
		got, ok := out[file]
		if !ok {
			t.Fatalf("%s is not rendered", file)
		}
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("%s lacks %q:\n%s", file, w, got)
			}
		}
		return got
	}
	// one pod: Haus and qmd share loopback; only Haus's port is published
	pod := has("quadlet/haus/haus.pod", "PodName=haus", "PublishPort=8650:8650")
	if strings.Contains(pod, "8181") {
		t.Errorf("the pod publishes qmd's port; it has no auth and stays on loopback:\n%s", pod)
	}
	haus := has("quadlet/haus/haus.container", "Pod=haus.pod", "Environment=HAUS_QMD_URL=http://127.0.0.1:8181", "Environment=HAUS_QMD_COLLECTION=notizen", "Environment=HAUS_NOTES_DIR=/notes",
		"Volume=/d/file-share/data/notes:/notes:")
	if strings.Contains(haus, "PublishPort") {
		t.Errorf("haus.container publishes a port itself; in a pod the pod does:\n%s", haus)
	}
	qmd := has("quadlet/haus/haus-qmd.container",
		"Pod=haus.pod",
		"Volume=./qmd.json:/etc/qmd/index.yml:ro",
		"Volume=/d/haus-qmd:/cache:Z",
		"Volume=/d/file-share/data/notes:/notes:ro",
		"HealthCmd=qmd-health", "Notify=healthy", "HealthOnFailure=kill")
	for _, bad := range []string{"PublishPort", "AddDevice", "0.0.0.0", "Network=host"} {
		if strings.Contains(qmd, bad) {
			t.Errorf("haus-qmd.container carries %s: loopback and CPU only", bad)
		}
	}
	if m := regexp.MustCompile(`(?m)^Image=(.+)$`).FindStringSubmatch(qmd); m == nil || !strings.HasPrefix(m[1], "ghcr.io/mdopp/laib-qmd:2.8.3") {
		t.Errorf("qmd's image is not pinned to 2.8.3: %v", m)
	}
	// index.yml (JSON is YAML): the notes folder, and models.embed pinned — it overrides QMD_EMBED_MODEL
	var cfg struct {
		Collections map[string]struct{ Path, Pattern string }
		Models      map[string]string
	}
	if err := json.Unmarshal([]byte(has("quadlet/haus/qmd.json")), &cfg); err != nil {
		t.Fatalf("qmd.json: %v", err)
	}
	if c, ok := cfg.Collections["notizen"]; !ok || c.Path != "/notes" || c.Pattern != "**/*.md" {
		t.Errorf("the notizen collection (Haus's default HAUS_QMD_COLLECTION) is not the mounted notes folder: %+v", cfg.Collections)
	}
	if cfg.Models["embed"] != "hf:ggml-org/embeddinggemma-300M-GGUF/embeddinggemma-300M-Q8_0.gguf" {
		t.Errorf("models.embed is not pinned to embeddinggemma-300M: %q", cfg.Models["embed"])
	}
	// the refresh: a timer that is enabled, a oneshot it fires that is not
	timer := has("systemd/haus-qmd-refresh.timer", "[Timer]", "OnCalendar=", "WantedBy=timers.target")
	if strings.Contains(timer, "Persistent=true") {
		t.Errorf("a missed refresh is caught by the next one; no catch-up burst after boot:\n%s", timer)
	}
	svc := has("systemd/haus-qmd-refresh.service", "Type=oneshot", "TimeoutStartSec=")
	if strings.Contains(svc, "[Install]") {
		t.Errorf("the refresh is started by its timer only:\n%s", svc)
	}
	var steps []string
	for _, m := range regexp.MustCompile(`(?m)^ExecStart=(.*)$`).FindAllStringSubmatch(svc, -1) {
		steps = append(steps, m[1])
	}
	// #219: the documents' export is a unit of its own the refresh runs after (haus219_test.go)
	want := []string{"/usr/bin/podman exec systemd-haus-qmd qmd update", "/usr/bin/podman exec systemd-haus-qmd qmd embed"}
	if strings.Join(steps, "\n") != strings.Join(want, "\n") {
		t.Errorf("the refresh runs %q, want update then embed, each failing the unit:\n%s", steps, svc)
	}
	for p, c := range out {
		if !strings.Contains(p, "haus") {
			continue
		}
		for _, l := range strings.Split(c, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(l), "#") && regexp.MustCompile(`qmd\s+pull`).MatchString(l) {
				t.Errorf("%s runs qmd pull, which deletes the cached models offline: %s", p, l)
			}
		}
	}
	// the library's data is a declared data path of haus, kept out of the backup (a cache)
	var declared bool
	for _, d := range st.Services["haus"].Contract.Data {
		if d.Path == "${DATA_DIR}/haus-qmd" && d.Backup != nil && !*d.Backup {
			declared = true
		}
	}
	if !declared {
		t.Error("${DATA_DIR}/haus-qmd is not a declared data path with backup: false")
	}
	if got := strings.Join(st.Services["haus"].UnitNames(), ","); got != "haus-pod.service,haus-qmd-refresh.timer,haus-qmd.service,haus.service" {
		t.Errorf("haus's units are %s: the timer is one, its oneshot is not", got)
	}
	if got := strings.Join(st.Services["haus"].Oneshots(), ","); got != "haus-documents-export.service,haus-qmd-refresh.service" {
		t.Errorf("haus's oneshots are %q", got)
	}
}
