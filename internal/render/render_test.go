package render

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// TestGolden renders every testdata/golden/<case>/state and compares with
// <case>/expected. UPDATE_GOLDEN=1 rewrites the expected tree.
func TestGolden(t *testing.T) {
	cases, _ := filepath.Glob("../../testdata/golden/*")
	if len(cases) == 0 {
		t.Fatal("no golden cases")
	}
	for _, dir := range cases {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			st, err := state.Load(filepath.Join(dir, "state"))
			if err != nil {
				t.Fatal(err)
			}
			out, err := Render(st, nil)
			if err != nil {
				t.Fatal(err)
			}
			exp := filepath.Join(dir, "expected")
			if os.Getenv("UPDATE_GOLDEN") == "1" {
				os.RemoveAll(exp)
				for p, c := range out {
					full := filepath.Join(exp, p)
					os.MkdirAll(filepath.Dir(full), 0o755)
					os.WriteFile(full, []byte(c), 0o644)
				}
				return
			}
			var want []string
			filepath.Walk(exp, func(p string, info os.FileInfo, err error) error {
				if err == nil && !info.IsDir() {
					rel, _ := filepath.Rel(exp, p)
					want = append(want, rel)
				}
				return nil
			})
			sort.Strings(want)
			var got []string
			for p := range out {
				got = append(got, p)
			}
			sort.Strings(got)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("file set differs\n got %v\nwant %v", got, want)
			}
			for _, p := range want {
				b, _ := os.ReadFile(filepath.Join(exp, p))
				if string(b) != out[p] {
					t.Errorf("%s differs:\n--- want\n%s\n--- got\n%s", p, b, out[p])
				}
			}
		})
	}
}

func TestRedactHidesSecrets(t *testing.T) {
	out := Output{"x": "password=hunter2 other=hunter2"}
	red := Redact(out, map[string]string{"PW": "hunter2"})
	if strings.Contains(red["x"], "hunter2") {
		t.Fatal("secret leaked")
	}
	if red["x"] != "password=${secret:PW} other=${secret:PW}" {
		t.Fatal(red["x"])
	}
}

func TestClientSecretHashIsDeterministicAndVerifiable(t *testing.T) {
	a := PBKDF2SHA512("hunter2", "whoami")
	b := PBKDF2SHA512("hunter2", "whoami")
	if a != b {
		t.Fatal("hash changes between renders; plan would never be quiet")
	}
	if !strings.HasPrefix(a, "$pbkdf2-sha512$310000$") || strings.Contains(a, "+") || strings.Contains(a, "=") {
		t.Fatal(a)
	}
	if PBKDF2SHA512("hunter2", "other") == a {
		t.Fatal("salt must depend on the client")
	}
	out, _ := Render(mustLoad(t), map[string]string{"OIDC_WHOAMI_SECRET": "hunter2"})
	if !strings.Contains(out["authelia/laib-clients.yml"], "$pbkdf2-sha512$") || strings.Contains(out["authelia/laib-clients.yml"], "hunter2") {
		t.Fatal(out["authelia/laib-clients.yml"])
	}
}

func mustLoad(t *testing.T) *state.State {
	t.Helper()
	st, err := state.Load("../../testdata/golden/basic/state")
	if err != nil {
		t.Fatal(err)
	}
	return st
}
