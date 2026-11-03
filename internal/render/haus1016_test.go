package render

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// laib#1016 (haus#209): Haus's container reaches every person's archive.
// The shipped box with anna's archive and a second one for max-moritz
// (portOffset 1): HAUS_ARCHIV_INTERN names both with their ports, each
// archive's ARCHIV_HAUS_SECRET_<PERSON> reaches Haus as
// HAUS_ARCHIV_SECRET_<PERSON> only, as a podman secret, never in a file.
func archivBox(t *testing.T, more bool) *state.State {
	t.Helper()
	repo := shippedBox(t).Dir
	if more {
		src, dst := filepath.Join(repo, "services", "archiv-anna"), filepath.Join(repo, "services", "archiv-max-moritz")
		os.MkdirAll(dst, 0o755)
		entries, _ := os.ReadDir(src)
		for _, e := range entries {
			b, _ := os.ReadFile(filepath.Join(src, e.Name()))
			if e.Name() == "laib.yml" {
				b = regexp.MustCompile(`(?m)^name: .*\nperson: anna\nportOffset: 0$`).ReplaceAll(b, []byte("name: archiv-max-moritz\nperson: max-moritz\nportOffset: 1"))
			}
			if !e.IsDir() {
				os.WriteFile(filepath.Join(dst, e.Name()), b, 0o644)
			}
		}
		box, _ := os.ReadFile(filepath.Join(repo, "box.yml"))
		os.WriteFile(filepath.Join(repo, "box.yml"), []byte(strings.Replace(string(box), "people: {", `people: {max-moritz: ["signal:+4915100000009"], `, 1)), 0o644)
	}
	st, err := state.Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestHausReachesEveryPersonsArchive(t *testing.T) {
	st := archivBox(t, true)
	secrets := map[string]string{"ARCHIV_HAUS_SECRET_ANNA": "anna-haus-v1016", "ARCHIV_HAUS_SECRET_MAX_MORITZ": "maxm-haus-v1016"}
	out, err := Render(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	unit := out["quadlet/haus/haus.container"]
	if !strings.Contains(unit, "\n"+`Environment="HAUS_ARCHIV_INTERN=anna=http://host.containers.internal:18540 max-moritz=http://host.containers.internal:18541"`+"\n") {
		t.Errorf("HAUS_ARCHIV_INTERN does not name both archives with their ports:\n%s", unit)
	}
	if !strings.Contains(out["quadlet/archiv-max-moritz/archiv-max-moritz.container"], "\nPublishPort=18541:8080\n") {
		t.Errorf("max-moritz's archive is not on 18541 for Haus's pod to reach:\n%s", out["quadlet/archiv-max-moritz/archiv-max-moritz.container"])
	}
	// each secret under its own name only, in Haus's container only, and never as text in any file
	sec := PodmanSecrets(st, secrets)
	want := map[string]string{"HAUS_ARCHIV_SECRET_ANNA": "anna-haus-v1016", "HAUS_ARCHIV_SECRET_MAX_MORITZ": "maxm-haus-v1016"}
	for key, val := range want {
		name := podmanSecretName("haus", "haus", key)
		if sec[name] != val || !strings.Contains(unit, "\nSecret="+name+",type=env,target="+key+"\n") {
			t.Errorf("Haus's %s is not that archive's secret as a podman secret (%q):\n%s", key, sec[name], unit)
		}
	}
	for name, val := range sec {
		for key, v := range want {
			if val == v && name != podmanSecretName("haus", "haus", key) && !strings.HasPrefix(name, "laib.archiv-") {
				t.Errorf("%s carries %s too", name, key)
			}
		}
	}
	real, _ := Render(st, secrets)
	for file, content := range real {
		for _, v := range want {
			if strings.Contains(content, v) {
				t.Errorf("%s carries a secret's value", file)
			}
		}
	}
	if strings.Contains(out["quadlet/haus/haus-qmd.container"], "ARCHIV") {
		t.Error("haus-qmd gets the archives too; only haus.container asked")
	}
	// a new secret value applies Haus again
	if u := SecretUsers(st, "ARCHIV_HAUS_SECRET_MAX_MORITZ"); !strings.Contains(strings.Join(u, " "), "haus") {
		t.Errorf("a new ARCHIV_HAUS_SECRET_MAX_MORITZ does not apply Haus again: %v", u)
	}
	// adding an archive changes Haus's unit: Haus applies again
	one, err := Render(archivBox(t, false), nil)
	if err != nil {
		t.Fatal(err)
	}
	if one["quadlet/haus/haus.container"] == unit || strings.Contains(one["quadlet/haus/haus.container"], "max-moritz") {
		t.Errorf("one archive less renders the same Haus:\n%s", one["quadlet/haus/haus.container"])
	}
}
