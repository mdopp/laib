package state

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// #986: the golden case perperson, martin's and anna's instance of the
// test template notizbuch (testdata/perperson), copied so a case can break it.
func perPerson986(t *testing.T) string {
	t.Helper()
	src, dst := "../../testdata/golden/perperson/state", t.TempDir()
	filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		rel, _ := filepath.Rel(src, p)
		if info.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, _ := os.ReadFile(p)
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	return dst
}

// Two persons' instances of one template meet nowhere: their units (and so
// their containers, systemd-<unit>), secrets, host ports and data are their
// own, and the template's purpose is one service, no HINWEIS.
func TestTwoPersonsInstancesNeverMeet(t *testing.T) {
	st, err := Load(perPerson986(t))
	if err != nil {
		t.Fatal(err)
	}
	martin, anna := st.Services["notizbuch-martin"], st.Services["notizbuch-anna"]
	if got := strings.Join(anna.UnitNames(), " "); got != "notizbuch-anna-sync.timer notizbuch-anna.service" {
		t.Errorf("anna's units %s", got)
	}
	if slices.ContainsFunc(anna.UnitNames(), func(u string) bool { return slices.Contains(martin.UnitNames(), u) }) {
		t.Errorf("a unit of both: %v %v", anna.UnitNames(), martin.UnitNames())
	}
	if !slices.Equal(anna.Oneshots(), []string{"notizbuch-anna-sync.service"}) {
		t.Errorf("anna's oneshot %v", anna.Oneshots())
	}
	if anna.Contract.Secrets[0].Name != "NOTIZBUCH_KEY_ANNA" || anna.Values["KEY"].Secret != "NOTIZBUCH_KEY_ANNA" || anna.Contract.Routes[0].ProxySecret != "NOTIZBUCH_KEY_ANNA" || martin.Contract.Secrets[0].Name != "NOTIZBUCH_KEY_MARTIN" {
		t.Errorf("secrets %v %v %v", anna.Contract.Secrets, anna.Values, martin.Contract.Secrets)
	}
	if anna.Contract.Routes[0].Port != 8481 || anna.Contract.Ports[0].Port != 8481 || anna.Contract.Health.HTTP != "http://127.0.0.1:8481/health" || !strings.Contains(anna.Units["notizbuch-anna.container"], "PublishPort=127.0.0.1:8481:8080\n") {
		t.Errorf("anna's ports %+v %+v %s", anna.Contract.Routes, anna.Contract.Ports, anna.Contract.Health.HTTP)
	}
	if martin.Contract.Routes[0].Port != 8480 || DataPaths(st, anna)[0] != "/mnt/data/stacks/notizbuch-anna" || DataPaths(st, martin)[0] != "/mnt/data/stacks/notizbuch-martin" {
		t.Errorf("martin's port %d, data %v %v", martin.Contract.Routes[0].Port, DataPaths(st, anna), DataPaths(st, martin))
	}
	if len(st.Warnings) != 0 {
		t.Errorf("instances of one template are no second service for a purpose: %v", st.Warnings)
	}
	// the HINWEIS still fires for a second service that is not an instance of it
	anna.Contract.Template.Name = "anderes-notizbuch"
	if len(st.purposeWarnings()) != 1 {
		t.Error("two templates for one purpose must still be a HINWEIS")
	}
}

// What would make two instances meet, or an instance nobody's, the loader refuses.
func TestTheLoaderRefusesAnInstanceThatMeetsAnother(t *testing.T) {
	edit := func(repo, file, from, to string) {
		for _, p := range []string{"martin", "anna"} {
			f := filepath.Join(repo, "services", "notizbuch-"+p, file)
			b, _ := os.ReadFile(f)
			os.WriteFile(f, []byte(strings.ReplaceAll(string(b), from, to)), 0o644)
		}
	}
	for want, breakIt := range map[string]func(string){
		"both make the unit sync.timer": func(r string) {
			for _, p := range []string{"martin", "anna"} {
				os.Rename(filepath.Join(r, "services", "notizbuch-"+p, "notizbuch-sync.timer"), filepath.Join(r, "services", "notizbuch-"+p, "sync.timer"))
			}
		},
		"both keep ${DATA_DIR}/notizbuch":             func(r string) { edit(r, "laib.yml", "${DATA_DIR}/${SERVICE}", "${DATA_DIR}/notizbuch") },
		`person "anna" is not in box.yml people`:      func(r string) { edit(r, "../../box.yml", "  anna:", "  anne:") },
		"person: on a template that is not perPerson": func(r string) { edit(r, "laib.yml", "perPerson: true", "") },
		"a per-person template is installed for one":  func(r string) { edit(r, "laib.yml", "person: anna\n", "") },
		"a person's route has a login and no bypass":  func(r string) { edit(r, "laib.yml", "group: family", "group: family\n    bypass: [/api/]") },
		`host "notizen" already published by notizbuch-`: func(r string) {
			f := filepath.Join(r, "services", "notizbuch-anna", "laib.yml")
			b, _ := os.ReadFile(f)
			os.WriteFile(f, []byte(strings.Replace(string(b), "name: notizbuch, version", "name: anders, version", 1)), 0o644)
		},
	} {
		repo := perPerson986(t)
		breakIt(repo)
		if _, err := Load(repo); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want %q, got %v", want, err)
		}
	}
}
