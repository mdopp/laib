package main

import (
	"bytes"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/expected from the fixture")

// fixture copies testdata/tree to a temp dir: a small laib tree with two
// templates, two kernel packages, a helper program and both sources.
func fixture(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir("testdata/tree", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("testdata/tree", p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func runIn(t *testing.T, root string, args ...string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	code := run(args, root, &out, &out)
	return code, out.String()
}

func read(t *testing.T, root, p string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, p))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func edit(t *testing.T, root, p, old, new string) {
	t.Helper()
	s := read(t, root, p)
	if !strings.Contains(s, old) {
		t.Fatalf("%s has no %q", p, old)
	}
	os.WriteFile(filepath.Join(root, p), []byte(strings.Replace(s, old, new, 1)), 0o644)
}

// The pages are what the fixture's code says: the edges of laib.yml, the
// layers, the routes, ports, hands, secrets and ties, and the same bytes
// on every run.
func TestPagesFromTheFixture(t *testing.T) {
	root := fixture(t)
	if code, out := runIn(t, root); code != 0 {
		t.Fatalf("c4: %d\n%s", code, out)
	}
	c4, sec, readme := read(t, root, "docs/C4.md"), read(t, root, "docs/SICHERHEIT.md"), read(t, root, "README.md")
	for _, want := range []string{
		`    t_a["Alpha<br/>Fotos der Familie"]:::s1`,
		`    t_b["Beta<br/>Seiten der Familie"]:::s2`,
		"  t_b --> t_a\n",               // after:, and uses: [a] is not drawn twice
		"  t_b ==>|Tür| laib\n",         // needs: [mcp]
		"  p_helper ==>|Tür| laib\n",    // a ring-0b helper
		"  k_cmd_laib --> k_internal_x", // imports, cmd/laib → state implied through x
		`k_internal_x -.->|"das Lesen, #7"| p_helper`,
		"| [Beta](../templates/b/) `b` | 2 | Seiten der Familie | `a` | `a` |",
	} {
		if !strings.Contains(c4, want) {
			t.Errorf("docs/C4.md lacks %q", want)
		}
	}
	if strings.Contains(c4, "k_cmd_laib --> k_internal_state") {
		t.Error("an import another path implies is drawn")
	}
	for _, want := range []string{
		"| www.&lt;domain&gt; | `b` | 2 | von außen | Authelia, ein Faktor, von außen zwei | ohne Authelia: `/napi/` |",
		"| fotos.&lt;domain&gt; | `a` | 1 | Heimnetz | Authelia, ein Faktor | nur Gruppe `family` |",
		"| `a` | 1000 | Alpha |",
		"| `helper` | propose | `propose`, `note` | — | — | fest im Code |",
		"| `b` | — | `B_LAIB_TOKEN` (Token der Hand `b`) |",
		"| `a` | `A_DB_PASSWORD` | — |",
		"| `b/b.container` | 2 | Label aus | reads a's photos |",
		"| `b` | `b.container` | fremder Ordner `${DATA_DIR}/a` |",
		"| `b` | `b-refresh.service` | Host-Unit mit podman |",
		"**Lücke**: unverschlüsselt ([#9](https://github.com/mdopp/laib/issues/9))",
		"geplant ([#9](https://github.com/mdopp/laib/issues/9), [archiv#3](https://github.com/mdopp/archiv/issues/3))",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("docs/SICHERHEIT.md lacks %q", want)
		}
	}
	if !strings.Contains(readme, "- Dependencies point down.\n") || !strings.HasSuffix(readme, "<!-- c4:end -->\n\nThe end.\n") {
		t.Errorf("README.md's section:\n%s", readme)
	}
	golden(t, "C4.md", c4)
	golden(t, "SICHERHEIT.md", sec)
	if code, out := runIn(t, root, "--check"); code != 0 {
		t.Errorf("a second run is not the same bytes: %s", out)
	}
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	p := filepath.Join("testdata", "expected", name)
	if *update {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(got), 0o644)
	}
	want, _ := os.ReadFile(p)
	if string(want) != got {
		t.Errorf("%s differs from %s (go test ./scripts/c4 -run TestPages -update)", name, p)
	}
}

// --check fails on a page the code has outrun, says what to run, and
// writes nothing.
func TestCheckFailsOnAStalePage(t *testing.T) {
	root := fixture(t)
	runIn(t, root)
	before := read(t, root, "docs/C4.md")
	edit(t, root, "templates/a/laib.yml", "title: Alpha", "title: Alfa")
	code, out := runIn(t, root, "--check")
	if code != 1 || !strings.Contains(out, "docs/C4.md veraltet") || !strings.Contains(out, "Führe scripts/c4.sh aus") {
		t.Fatalf("--check: %d\n%s", code, out)
	}
	if read(t, root, "docs/C4.md") != before {
		t.Error("--check wrote the page")
	}
}

// A template without its layer, a kernel package without its place and a
// point that is not „läuft“ without an issue fail the generation.
func TestTheSourcesAreHeldToTheCode(t *testing.T) {
	root := fixture(t)
	edit(t, root, "docs/c4.yml", "  b: 2\n", "")
	edit(t, root, "docs/c4.yml", "  internal/state: {titel: Zustand, text: laden}\n", "")
	edit(t, root, "docs/sicherheit.yml", `issues: ["9", "archiv#3"]`, "issues: []")
	code, out := runIn(t, root, "--check")
	for _, want := range []string{"die Vorlage b hat keine Schicht", "das Kern-Paket internal/state hat keinen Eintrag", "„Platte verschlüsseln“ steht auf geplant und nennt kein Issue"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	if code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
}

func gateRun(t *testing.T, head, base string, labels string) (int, string) {
	t.Helper()
	t.Setenv("C4_LABELS", labels)
	return runIn(t, head, "--gate", base)
}

// The judgment gate: a new public route without a word in
// docs/sicherheit.yml fails and names it; acknowledged with its reason it
// passes and the acknowledgement is the trace.
func TestTheGateWantsAJudgmentForANewPublicRoute(t *testing.T) {
	base, head := fixture(t), fixture(t)
	route := "routes:\n  - host: fotos\n"
	edit(t, head, "templates/a/laib.yml", route, "routes:\n  - host: teilen\n    port: 1001\n    exposure: public\n    auth: none\n  - host: fotos\n")
	code, out := gateRun(t, head, base, "")
	if code != 1 || !strings.Contains(out, "neu: Adresse und Anmeldung `route a teilen` (public auth=none") || !strings.Contains(out, "ordne sie in docs/sicherheit.yml ein") {
		t.Fatalf("gate: %d\n%s", code, out)
	}
	edit(t, head, "docs/sicherheit.yml", "aenderungen: []", "aenderungen:\n  - fakt: route a teilen\n    grund: Gäste sehen nur ein geteiltes Album, wie die Gast-Apps im Konzept\n")
	if code, out := gateRun(t, head, base, ""); code != 0 {
		t.Fatalf("acknowledged: %d\n%s", code, out)
	}
	runIn(t, head)
	if !strings.Contains(read(t, head, "docs/SICHERHEIT.md"), "| `route a teilen` | Gäste sehen nur ein geteiltes Album") {
		t.Error("the acknowledgement leaves no trace on the page")
	}
}

// A judgment in sicherheit.yml itself also lets the change pass; a new
// internal route behind Authelia is no security change.
func TestTheGatePassesAJudgedChange(t *testing.T) {
	base, head := fixture(t), fixture(t)
	edit(t, head, "templates/a/laib.yml", "ports:\n", "ports:\n  - {port: 1002, lan: true}\n")
	if code, out := gateRun(t, head, base, ""); code != 1 || !strings.Contains(out, "`port a 1002`") {
		t.Fatalf("a new LAN port: %d\n%s", code, out)
	}
	edit(t, head, "docs/sicherheit.yml", "schutz: Caddy", "schutz: Caddy; Port 1002 für das Gerät im Flur")
	if code, out := gateRun(t, head, base, ""); code != 0 {
		t.Fatalf("judged: %d\n%s", code, out)
	}
	base, head = fixture(t), fixture(t)
	edit(t, head, "templates/a/laib.yml", "routes:\n", "routes:\n  - host: intern\n    port: 1003\n    exposure: internal\n    auth: one_factor\n")
	if code, out := gateRun(t, head, base, ""); code != 0 {
		t.Fatalf("an internal route behind Authelia: %d\n%s", code, out)
	}
}

// The principles are the owner's: changed without the label, red.
func TestPrinciplesChangeOnlyWithTheOwnersLabel(t *testing.T) {
	base, head := fixture(t), fixture(t)
	edit(t, head, "docs/sicherheit.yml", "text: Abhängigkeiten zeigen nach unten.", "text: Abhängigkeiten zeigen meistens nach unten.")
	code, out := gateRun(t, head, base, "docs")
	if code != 1 || !strings.Contains(out, "besitzer-entscheidung") {
		t.Fatalf("without the label: %d\n%s", code, out)
	}
	if code, out := gateRun(t, head, base, "docs,besitzer-entscheidung"); code != 0 {
		t.Fatalf("with the label: %d\n%s", code, out)
	}
}

// The daily check: a point still „geplant“ whose issue is closed is red,
// and says which; without gh it skips with a notice.
func TestIssuesFindsAClosedIssueBehindAnOpenPoint(t *testing.T) {
	root := fixture(t)
	found, state := ghFound, ghState
	t.Cleanup(func() { ghFound, ghState = found, state })
	asked := map[string]bool{}
	ghFound = func() bool { return true }
	ghState = func(repo, n string) (string, error) {
		asked[repo+"#"+n] = true
		if repo == "mdopp/archiv" {
			return "CLOSED", nil
		}
		return "OPEN", nil
	}
	code, out := runIn(t, root, "--issues")
	if code != 1 || !strings.Contains(out, "offener Punkt „Platte verschlüsseln“ steht auf **geplant**, nennt aber [archiv#3](https://github.com/mdopp/archiv/issues/3)") {
		t.Fatalf("--issues: %d\n%s", code, out)
	}
	if !asked["mdopp/laib#9"] {
		t.Errorf("asked %v, not mdopp/laib#9", asked)
	}
	ghFound = func() bool { return false }
	if code, out := runIn(t, root, "--issues"); code != 0 || !strings.Contains(out, "gh fehlt") {
		t.Errorf("without gh: %d\n%s", code, out)
	}
}
