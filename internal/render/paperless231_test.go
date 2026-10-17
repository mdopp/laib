package render

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// #231: Paperless logs the family in through Authelia's OIDC (django-allauth's
// openid_connect), not a trusted Remote-User: one login, an account made on
// the first visit, and the apps keep their tokens. The client secret reaches
// the container only as a podman secret, rendered on every apply, so it
// cannot drift from Authelia's (ServiceBay's Immich and ABS lesson).
func TestPaperlessLogsInThroughAutheliaOIDC(t *testing.T) {
	dir := t.TempDir()
	copyTree(t, "../../testdata/golden/basic/state", dir)
	svc := filepath.Join(dir, "services", "paperless")
	copyTree(t, "../../templates/paperless", svc)
	os.Rename(filepath.Join(svc, "values.example.yml"), filepath.Join(svc, "values.yml"))
	y, _ := os.ReadFile(filepath.Join(svc, "laib.yml")) // the file share is not this test's business
	os.WriteFile(filepath.Join(svc, "laib.yml"), []byte(strings.Replace(string(y), "after: [auth, file-share]", "after: [auth]", 1)), 0o644)
	st, err := state.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	secrets := map[string]string{}
	for _, s := range SecretNames(st) {
		secrets[s.Name] = "v-" + s.Name
	}
	out, err := Render(st, secrets)
	if err != nil {
		t.Fatal(err)
	}
	if c := out["authelia/laib-clients.yml"]; !strings.Contains(c, "client_id: paperless") || !strings.Contains(c, "https://paperless.example.home/accounts/oidc/authelia/login/callback/") {
		t.Errorf("no Authelia client for paperless with allauth's callback:\n%s", c)
	}
	unit := out["quadlet/paperless/paperless.container"]
	for _, want := range []string{"PAPERLESS_APPS=allauth.socialaccount.providers.openid_connect", "PAPERLESS_SOCIAL_AUTO_SIGNUP=true", ",type=env,target=PAPERLESS_SOCIALACCOUNT_PROVIDERS"} {
		if !strings.Contains(unit, want) {
			t.Errorf("paperless.container: no %s", want)
		}
	}
	if strings.Contains(unit, "v-OIDC_PAPERLESS_SECRET") || strings.Contains(unit, "REMOTE_USER") {
		t.Errorf("paperless.container carries the client secret or trusts Remote-User:\n%s", unit)
	}
	if !strings.Contains(out["quadlet/paperless/paperless.pod"], "AddHost=auth.example.home:") {
		t.Error("paperless.pod: the pod does not resolve auth.<domain> (AddHost belongs on the pod)")
	}
	var providers struct {
		OpenIDConnect struct {
			Apps []struct {
				ProviderID string `json:"provider_id"`
				ClientID   string `json:"client_id"`
				Secret     string `json:"secret"`
				Settings   struct {
					ServerURL string `json:"server_url"`
				} `json:"settings"`
			} `json:"APPS"`
		} `json:"openid_connect"`
	}
	found := false
	for _, v := range PodmanSecrets(st, secrets) {
		if !strings.Contains(v, "openid_connect") {
			continue
		}
		found = true
		if err := json.Unmarshal([]byte(v), &providers); err != nil {
			t.Fatalf("PAPERLESS_SOCIALACCOUNT_PROVIDERS is no JSON: %v\n%s", err, v)
		}
	}
	if !found || len(providers.OpenIDConnect.Apps) != 1 {
		t.Fatalf("no openid_connect app in a podman secret")
	}
	a := providers.OpenIDConnect.Apps[0]
	if a.ProviderID != "authelia" || a.ClientID != "paperless" || a.Secret != "v-OIDC_PAPERLESS_SECRET" || a.Settings.ServerURL != "https://auth.example.home" {
		t.Errorf("openid_connect app: %+v", a)
	}
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if info.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, _ := os.ReadFile(p)
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
}
