package render

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// laib#1016 (haus#209): Homes's container reaches every person's archive.
// The shipped box with anna's archive and a second one for max-moritz
// (portOffset 1): HOMES_ARCHIV_INTERN names both with their ports, each
// archive's ARCHIV_HOMES_SECRET_<PERSON> reaches Homes as
// HOMES_ARCHIV_SECRET_<PERSON> only, as a podman secret, never in a file.
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

func TestHomesReachesEveryPersonsArchive(t *testing.T) {
	st := archivBox(t, true)
	secrets := map[string]string{"ARCHIV_HOMES_SECRET_ANNA": "anna-homes-v1016", "ARCHIV_HOMES_SECRET_MAX_MORITZ": "maxm-homes-v1016"}
	out, err := Render(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	unit := out["quadlet/homes/homes.container"]
	if !strings.Contains(unit, "\n"+`Environment="HOMES_ARCHIV_INTERN=anna=http://host.containers.internal:18540 max-moritz=http://host.containers.internal:18541"`+"\n") {
		t.Errorf("HOMES_ARCHIV_INTERN does not name both archives with their ports:\n%s", unit)
	}
	if !strings.Contains(out["quadlet/archiv-max-moritz/archiv-max-moritz.container"], "\nPublishPort=18541:8080\n") {
		t.Errorf("max-moritz's archive is not on 18541 for Homes's pod to reach:\n%s", out["quadlet/archiv-max-moritz/archiv-max-moritz.container"])
	}
	// each secret under its own name only, in Homes's container only, and never as text in any file
	sec := PodmanSecrets(st, secrets)
	want := map[string]string{"HOMES_ARCHIV_SECRET_ANNA": "anna-homes-v1016", "HOMES_ARCHIV_SECRET_MAX_MORITZ": "maxm-homes-v1016"}
	for key, val := range want {
		name := podmanSecretName("homes", "homes", key)
		if sec[name] != val || !strings.Contains(unit, "\nSecret="+name+",type=env,target="+key+"\n") {
			t.Errorf("Homes's %s is not that archive's secret as a podman secret (%q):\n%s", key, sec[name], unit)
		}
	}
	for name, val := range sec {
		for key, v := range want {
			if val == v && name != podmanSecretName("homes", "homes", key) && !strings.HasPrefix(name, "laib.archiv-") {
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
	if strings.Contains(out["quadlet/homes/homes-qmd.container"], "ARCHIV") {
		t.Error("homes-qmd gets the archives too; only homes.container asked")
	}
	// a new secret value applies Homes again
	if u := SecretUsers(st, "ARCHIV_HOMES_SECRET_MAX_MORITZ"); !strings.Contains(strings.Join(u, " "), "homes") {
		t.Errorf("a new ARCHIV_HOMES_SECRET_MAX_MORITZ does not apply Homes again: %v", u)
	}
	// adding an archive changes Homes's unit: Homes applies again
	one, err := Render(archivBox(t, false), nil)
	if err != nil {
		t.Fatal(err)
	}
	if one["quadlet/homes/homes.container"] == unit || strings.Contains(one["quadlet/homes/homes.container"], "max-moritz") {
		t.Errorf("one archive less renders the same Homes:\n%s", one["quadlet/homes/homes.container"])
	}
}
