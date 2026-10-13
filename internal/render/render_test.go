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

// #46: podman auto-update takes a snapshot first and never runs at the
// backup's hour by default.
func TestAutoUpdateSnapshotsFirstAndAvoidsTheBackupHour(t *testing.T) {
	st, err := state.Load(filepath.Join("..", "..", "testdata", "golden", "basic", "state"))
	if err != nil {
		t.Fatal(err)
	}
	st.Box.Updates.Days = []string{"Sat", "Sun"}
	st.Box.Updates.Time = ""
	out, err := Render(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out["systemd/podman-auto-update.service.d/laib.conf"], "ExecStartPre=/var/opt/laib/laib backup") {
		t.Fatalf("no snapshot before the image update:\n%s", out["systemd/podman-auto-update.service.d/laib.conf"])
	}
	if !strings.Contains(out["systemd/podman-auto-update.timer.d/laib.conf"], " 04:30:00") {
		t.Fatalf("the default update time must not be the backup's 03:00:\n%s", out["systemd/podman-auto-update.timer.d/laib.conf"])
	}
}

// #42 (2): on the GPU box the builtin MODEL_KEY is the model role's own
// key (secret MODEL_API_KEY, declared by the llama template), so the Bote,
// the Werkstatt and the Operator can take the lease; a remote model keeps
// box.yml keySecret.
func TestModelKeyIsTheLocalRolesKey(t *testing.T) {
	st := &state.State{Box: state.Box{Domain: "x.home", DataDir: "/d"}, Services: map[string]*state.Service{"llama": {Name: "llama"}}}
	st.Box.Roles.Model = "llama"
	vars, err := variables(st, st.Services["llama"], map[string]string{"MODEL_API_KEY": "k-local"})
	if err != nil {
		t.Fatal(err)
	}
	if vars["MODEL_KEY"] != "k-local" {
		t.Fatalf("MODEL_KEY on the GPU box must be the role's key: %q", vars["MODEL_KEY"])
	}
	st.Box.Roles.Model = ""
	st.Box.Model.URL, st.Box.Model.KeySecret = "http://10.0.0.20:11435/v1", "MODEL_API_KEY"
	vars, _ = variables(st, st.Services["llama"], map[string]string{"MODEL_API_KEY": "k-remote"})
	if vars["MODEL_KEY"] != "k-remote" {
		t.Fatalf("remote: %q", vars["MODEL_KEY"])
	}
}

// #40: a monthly restore test runs from a timer rendered with the backup.
func TestBackupCheckTimerIsRendered(t *testing.T) {
	st, err := state.Load(filepath.Join("..", "..", "testdata", "golden", "basic", "state"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	timer, ok := out["systemd/laib-backup-check.timer"]
	if !ok || !strings.Contains(timer, "OnCalendar=monthly") {
		t.Fatalf("no monthly restore-test timer: %q", timer)
	}
	if !strings.Contains(out["systemd/laib-backup-check.service"], "laib backup check") {
		t.Fatal("the check service must run laib backup check")
	}
}

// Without a service that declares oidc, the clients fragment must not open
// an identity_providers block: Authelia refuses one without jwks and
// clients and crash-loops (#36, VM run 38163402522).
func TestNoOIDCClientsRendersNoIdentityProvidersBlock(t *testing.T) {
	st, err := state.Load(filepath.Join("..", "..", "testdata", "golden", "basic", "state"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range st.Services {
		s.Contract.OIDC = nil
	}
	out, err := Render(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out["authelia/laib-clients.yml"], "identity_providers") {
		t.Fatalf("an empty oidc block:\n%s", out["authelia/laib-clients.yml"])
	}
}

// #102: `nft -f` with a table block on an existing table adds to it. A port
// that leaves the drop list (lan: true, or its service removed) stayed
// dropped until the next reboot, and every load appended the rule again.
// The file replaces the table in one transaction: declare it (so the
// delete never fails on a fresh boot), delete it, then the block.
func TestFirewallRulesetReplacesTheTableInsteadOfAddingToIt(t *testing.T) {
	st, err := state.Load(filepath.Join("..", "..", "testdata", "golden", "basic", "state"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	nft := out["nftables/laib.nft"]
	decl, del, block := strings.Index(nft, "table inet laib\n"), strings.Index(nft, "delete table inet laib\n"), strings.Index(nft, "table inet laib {")
	if decl < 0 || del < 0 || block < 0 || !(decl < del && del < block) {
		t.Fatalf("the ruleset does not replace the table (want: table inet laib / delete table inet laib / table inet laib { … }):\n%s", nft)
	}
}
