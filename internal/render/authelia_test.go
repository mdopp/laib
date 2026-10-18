package render

import (
	htmltemplate "html/template"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	texttemplate "text/template"

	"github.com/mdopp/laib/internal/state"
	"gopkg.in/yaml.v3"
)

// TestAutheliaPolicyOnTheRealBinary asks Authelia itself (`authelia
// access-control check-policy`) what it does with the config the box loads:
// the auth hook's configuration.yml and laib's rendered fragments, in
// X_AUTHELIA_CONFIG's order, for the routes of every built-in template.
// LAIB_AUTHELIA is the binary, taken from the image the auth template pins
// (ci.yml); without it the test cannot prove anything and skips.
//
// A family member is denied every admins route, an admin gets the factor the
// route declares, an LLDAP account in neither group (radicale's bind account)
// gets nothing, and the verdicts do not change when the files load in the
// other order (#249): Authelia replaces a list from an earlier file instead of
// appending to it, so a rule in the hook's config would silently decide or
// silently vanish depending on that order.
func TestAutheliaPolicyOnTheRealBinary(t *testing.T) {
	bin := os.Getenv("LAIB_AUTHELIA")
	if bin == "" {
		t.Skip("LAIB_AUTHELIA (the authelia binary) not set")
	}
	st, err := state.Load(filepath.Join("..", "..", "testdata", "golden", "nuc", "state"))
	if err != nil {
		t.Fatal(err)
	}
	st.Services = map[string]*state.Service{}
	dirs, _ := filepath.Glob("../../templates/*/laib.yml")
	secrets := map[string]string{}
	for _, f := range dirs {
		svc := &state.Service{Name: filepath.Base(filepath.Dir(f))}
		b, _ := os.ReadFile(f)
		if err := yaml.Unmarshal(b, &svc.Contract); err != nil {
			t.Fatal(f, err)
		}
		if o := svc.Contract.OIDC; o != nil {
			secrets[state.OIDCSecretName(o.ClientID)] = "test-secret-" + o.ClientID
		}
		st.Services[svc.Name] = svc
	}

	dir := t.TempDir()
	render := filepath.Join(dir, "render", "authelia")
	os.MkdirAll(render, 0o755)
	os.WriteFile(filepath.Join(render, "laib-access.yml"), []byte(autheliaAccess(st)), 0o644)
	os.WriteFile(filepath.Join(render, "laib-clients.yml"), []byte(autheliaClients(st, secrets)), 0o644)
	hook := exec.Command("python3", "../../templates/auth/pre-start")
	hook.Env = []string{"PATH=" + os.Getenv("PATH"), "LAIB_DATA_DIR=" + dir, "LAIB_DOMAIN=" + st.Box.Domain, "BASE_DN=dc=nuc,dc=home", "LAIB_RENDER_DIR=" + filepath.Join(dir, "render")}
	for _, s := range []string{"AUTHELIA_JWT_SECRET", "AUTHELIA_SESSION_SECRET", "AUTHELIA_STORAGE_KEY", "AUTHELIA_OIDC_HMAC_SECRET", "LLDAP_ADMIN_PASSWORD"} {
		hook.Env = append(hook.Env, "LAIB_SECRET_"+s+"=a-test-value-that-is-long-enough-for-authelia-0123456789")
	}
	if out, err := hook.CombinedOutput(); err != nil {
		t.Fatalf("auth pre-start: %v\n%s", err, out)
	}
	hostTemplates(t, filepath.Join(dir, "auth", "authelia-config"))

	// the files in the order the container loads them, /config and /laib mapped to the temp dir
	unit, _ := os.ReadFile("../../templates/auth/auth-authelia.container")
	m := regexp.MustCompile(`X_AUTHELIA_CONFIG=(\S+)`).FindSubmatch(unit)
	if m == nil {
		t.Fatal("auth-authelia.container: no X_AUTHELIA_CONFIG")
	}
	var files []string
	for _, f := range strings.Split(string(m[1]), ",") {
		f = strings.Replace(f, "/config/", filepath.Join(dir, "auth", "authelia-config")+"/", 1)
		files = append(files, strings.Replace(f, "/laib/", render+"/", 1))
	}
	if out, err := exec.Command(bin, append(configArgs(files), "validate-config")...).CombinedOutput(); err != nil {
		t.Fatalf("authelia refuses the box's config: %v\n%s", err, out)
	}
	reversed := make([]string, len(files))
	for i, f := range files {
		reversed[len(files)-1-i] = f
	}

	verdict := regexp.MustCompile(`The policy '(\w+)'`)
	check := func(host, path, group, want string) {
		t.Helper()
		for i, order := range [][]string{files, reversed} {
			args := append(configArgs(order), "access-control", "check-policy", "--url", "https://"+fqdn(st, host)+path)
			if group != "" {
				args = append(args, "--username", "u", "--groups", group)
			}
			out, err := exec.Command(bin, args...).CombinedOutput()
			got := verdict.FindSubmatch(out)
			if err != nil || got == nil {
				t.Fatalf("check-policy %s%s: %v\n%s", host, path, err, out)
			}
			if string(got[1]) != want {
				t.Errorf("%s%s as group %q (%s): Authelia applies %s, want %s", host, path, group, []string{"the box's file order", "files reversed"}[i], got[1], want)
			}
		}
	}

	check(st.Box.Laib.Host, "/", "admins", "one_factor")
	check(st.Box.Laib.Host, "/", "family", "deny")
	check("nowhere", "/", "admins", "deny") // a host no service routes
	n := 0
	for _, name := range st.ServiceNames() {
		for _, r := range st.Services[name].Contract.Routes {
			n++
			for _, p := range r.Bypass {
				check(r.Host, strings.TrimSuffix(p, "/")+"/x", "", "bypass")
			}
			if r.Auth == "none" {
				check(r.Host, "/", "", "bypass")
				continue
			}
			check(r.Host, "/", "admins", r.Auth)
			check(r.Host, "/", "lldap_strict_readonly", "deny")
			if r.Group == "admins" {
				check(r.Host, "/", "family", "deny")
			} else {
				check(r.Host, "/", "family", r.Auth)
			}
		}
	}
	if n < 10 {
		t.Fatalf("only %d routes across the built-in templates: the glob found the wrong files", n)
	}
}

func configArgs(files []string) []string {
	args := []string{"--config.experimental.filters", "template"}
	for _, f := range files {
		args = append(args, "--config", f)
	}
	return args
}

// TestAutheliaSMTPOnTheRealBinary: with a mail server, the hook's
// configuration.yml is one Authelia loads, and the password the container
// gets as a podman secret (X_LAIB_SMTP_PASSWORD, #359) is what Authelia's
// template filter puts into notifier.smtp.password, quotes, braces and
// trailing blanks included.
func TestAutheliaSMTPOnTheRealBinary(t *testing.T) {
	bin := os.Getenv("LAIB_AUTHELIA")
	if bin == "" {
		t.Skip("LAIB_AUTHELIA (the authelia binary) not set")
	}
	dir := t.TempDir()
	render := filepath.Join(dir, "render", "authelia")
	os.MkdirAll(render, 0o755)
	os.WriteFile(filepath.Join(render, "laib-clients.yml"), []byte("# none\n"), 0o644)
	os.WriteFile(filepath.Join(render, "laib-access.yml"), []byte("access_control:\n  default_policy: one_factor\n"), 0o644)
	hook := exec.Command("python3", "../../templates/auth/pre-start")
	hook.Env = []string{"PATH=" + os.Getenv("PATH"), "LAIB_DATA_DIR=" + dir, "LAIB_DOMAIN=vm.test", "BASE_DN=dc=vm,dc=test", "LAIB_RENDER_DIR=" + filepath.Join(dir, "render"),
		"SMTP_HOST=smtp.mail.test", "SMTP_PORT=587", "SMTP_SECURITY=starttls", "SMTP_USER=box@mail.test", "SMTP_FROM=Die Box <box@mail.test>", "SMTP_PASSWORD=set"}
	for _, s := range []string{"AUTHELIA_JWT_SECRET", "AUTHELIA_SESSION_SECRET", "AUTHELIA_STORAGE_KEY", "AUTHELIA_OIDC_HMAC_SECRET", "LLDAP_ADMIN_PASSWORD"} {
		hook.Env = append(hook.Env, "LAIB_SECRET_"+s+"=a-test-value-that-is-long-enough-for-authelia-0123456789")
	}
	if out, err := hook.CombinedOutput(); err != nil {
		t.Fatalf("auth pre-start: %v\n%s", err, out)
	}
	hostTemplates(t, filepath.Join(dir, "auth", "authelia-config"))
	cfg := filepath.Join(dir, "auth", "authelia-config")
	files := []string{filepath.Join(cfg, "configuration.yml"), filepath.Join(render, "laib-access.yml"), filepath.Join(cfg, "oidc-clients.yml")}
	pw := `pw-' {{ x }}"gm:ail  `
	run := func(args ...string) string {
		c := exec.Command(bin, append(configArgs(files), args...)...)
		c.Env = append(os.Environ(), "X_LAIB_SMTP_PASSWORD="+pw)
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("authelia %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("validate-config")
	if out := run("config", "template"); !strings.Contains(out, "\n      "+pw+"\n") || !strings.Contains(out, "address: 'submission://smtp.mail.test:587'") {
		t.Fatalf("the templated notifier does not carry the password as given:\n%s", out)
	}
}

// hostTemplates points the config's template_path (the container's
// /config/templates) at the folder pre-start wrote, so Authelia loads the
// German mail templates (#360) here as on the box.
func hostTemplates(t *testing.T, cfg string) {
	t.Helper()
	p := filepath.Join(cfg, "configuration.yml")
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "template_path: '/config/templates'") {
		t.Fatalf("configuration.yml has no template_path:\n%s", b)
	}
	os.WriteFile(p, []byte(strings.ReplaceAll(string(b), "'/config/templates'", "'"+filepath.Join(cfg, "templates")+"'")), 0o600)
}

// TestAutheliaMailTemplates: the German reset mail pre-start writes (#360)
// is a template Go parses, names only fields Authelia fills for
// IdentityVerificationJWT, and carries the link and the way to Haus.
func TestAutheliaMailTemplates(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "render", "authelia"), 0o755)
	hook := exec.Command("python3", "../../templates/auth/pre-start")
	hook.Env = []string{"PATH=" + os.Getenv("PATH"), "LAIB_DATA_DIR=" + dir, "LAIB_DOMAIN=vm.test", "BASE_DN=dc=vm,dc=test", "LAIB_RENDER_DIR=" + filepath.Join(dir, "render")}
	for _, s := range []string{"AUTHELIA_JWT_SECRET", "AUTHELIA_SESSION_SECRET", "AUTHELIA_STORAGE_KEY", "AUTHELIA_OIDC_HMAC_SECRET", "LLDAP_ADMIN_PASSWORD"} {
		hook.Env = append(hook.Env, "LAIB_SECRET_"+s+"=x")
	}
	if out, err := hook.CombinedOutput(); err != nil {
		t.Fatalf("auth pre-start: %v\n%s", err, out)
	}
	fields := struct{ Title, DisplayName, RemoteIP, Domain, LinkURL, LinkText, RevocationLinkURL, RevocationLinkText string }{
		"Reset your password", "Anna Muster", "127.0.0.1", "auth.vm.test", "https://auth.vm.test/reset-password/step2?token=t&x=1", "Reset", "https://auth.vm.test/revoke/reset-password?id=r", "Revoke"}
	for _, ext := range []string{"txt", "html"} {
		b, err := os.ReadFile(filepath.Join(dir, "auth", "authelia-config", "templates", "IdentityVerificationJWT."+ext))
		if err != nil {
			t.Fatal(err)
		}
		var out strings.Builder
		if ext == "txt" {
			err = texttemplate.Must(texttemplate.New(ext).Option("missingkey=error").Parse(string(b))).Execute(&out, fields)
		} else {
			err = htmltemplate.Must(htmltemplate.New(ext).Option("missingkey=error").Parse(string(b))).Execute(&out, fields)
		}
		if err != nil {
			t.Fatalf("%s: %v", ext, err)
		}
		for _, want := range []string{"Hallo Anna Muster", "https://haus.vm.test/willkommen", "24 Stunden", "revoke/reset-password"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("%s: no %q in\n%s", ext, want, out.String())
			}
		}
		if !strings.Contains(out.String(), "step2?token=t") {
			t.Errorf("%s: no link in\n%s", ext, out.String())
		}
	}
}
