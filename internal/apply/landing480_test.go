package apply

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/state"
)

// #480, #381: every https name a person is sent to — Authelia's landing
// after a login (default_redirection_url), the reset and invite mail, the
// Caddyfile's redirects and its denial pages — is a name the box serves.
// laib.<domain> after the box page moved to admin. was one that never
// existed; haus.<domain>/willkommen in the mail of a box without Haus is
// another.
func TestEveryLandingIsANameTheBoxServes(t *testing.T) {
	for _, haus := range []bool{false, true} {
		name := "without haus"
		if haus {
			name = "with haus"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			stDir := filepath.Join(home, "state")
			copyTree(t, "../../testdata/golden/nuc/state", stDir)
			hook, _ := os.ReadFile("../../templates/auth/pre-start")
			os.WriteFile(filepath.Join(stDir, "services", "auth", "pre-start"), hook, 0o755)
			if haus {
				copyTree(t, "../../templates/haus", filepath.Join(stDir, "services", "haus"))
				os.Rename(filepath.Join(stDir, "services", "haus", "values.example.yml"), filepath.Join(stDir, "services", "haus", "values.yml"))
			}
			box, _ := os.ReadFile(filepath.Join(stDir, "box.yml"))
			os.WriteFile(filepath.Join(stDir, "box.yml"), append(box, "laib: {host: admin, listen: 0.0.0.0}\n"...), 0o644)
			st, err := state.Load(stDir)
			if err != nil {
				t.Fatal(err)
			}
			out, err := render.Render(st, nil)
			if err != nil {
				t.Fatal(err)
			}
			rdir := filepath.Join(home, "render")
			for p, c := range out {
				os.MkdirAll(filepath.Dir(filepath.Join(rdir, p)), 0o755)
				os.WriteFile(filepath.Join(rdir, p), []byte(c), 0o644)
			}
			data := filepath.Join(home, "data")
			realHook := RunHook
			defer func() { RunHook = realHook }()
			RunHook = func(_ context.Context, h HookSpec) (string, error) {
				env, _ := os.ReadFile(h.EnvFile)
				extra := "\nLAIB_DATA_DIR=" + data + "\n"
				for _, s := range []string{"AUTHELIA_JWT_SECRET", "AUTHELIA_SESSION_SECRET", "AUTHELIA_STORAGE_KEY", "AUTHELIA_OIDC_HMAC_SECRET", "LLDAP_ADMIN_PASSWORD"} {
					extra += "LAIB_SECRET_" + s + "=x\n"
				}
				os.WriteFile(h.EnvFile, append(env, extra...), 0o600)
				return runSpecLocally(t, h)
			}
			c, ran := runHook(context.Background(), st, st.Services["auth"], boxfs.Paths{Home: home, Render: rdir}, "pre-start", nil, map[string]string{})
			if !ran || !c.OK {
				t.Fatalf("auth pre-start: %+v", c)
			}

			caddy := out["caddy/Caddyfile"]
			served := map[string]bool{}
			for _, l := range strings.Split(caddy, "\n") {
				if a, ok := strings.CutSuffix(l, " {"); ok && a != "" && !strings.ContainsAny(a[:1], "\t({*") {
					for _, h := range strings.Split(a, ", ") {
						served[h] = true
					}
				}
			}
			files := map[string]string{}
			for p, c := range out { // every rendered file: the Caddyfile, the units (Haus's link to the box page), the access rules
				files[p] = c
			}
			cfg := filepath.Join(data, "auth", "authelia-config")
			for _, g := range []string{filepath.Join(cfg, "*.yml"), filepath.Join(cfg, "templates", "*"), filepath.Join(rdir, "authelia", "*.yml")} {
				ms, _ := filepath.Glob(g)
				for _, m := range ms {
					b, _ := os.ReadFile(m)
					files[m] = string(b)
				}
			}
			if !strings.Contains(files[filepath.Join(cfg, "configuration.yml")], "default_redirection_url: 'https://") {
				t.Fatal("configuration.yml names no default_redirection_url")
			}
			url := regexp.MustCompile(`https://([a-z0-9.-]*` + regexp.QuoteMeta(st.Box.Domain) + `)\b`)
			seen := 0
			for f, body := range files {
				for _, m := range url.FindAllStringSubmatch(body, -1) {
					seen++
					if !served[m[1]] {
						t.Errorf("%s sends a person to https://%s, a name the box does not serve (served: %v)", filepath.Base(f), m[1], served)
					}
				}
			}
			if seen == 0 {
				t.Fatal("no https name found at all: the scan is broken")
			}
		})
	}
}
