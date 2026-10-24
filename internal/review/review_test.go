package review

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// golden is the basic state the render goldens use: caddy, auth (Authelia +
// LLDAP in a pod) and whoami behind them.
const golden = "../../testdata/golden/basic/state"

func copyTree(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
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

func write(t *testing.T, dir, rel, content string, mode fs.FileMode) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil { // WriteFile keeps the mode of an existing file
		t.Fatal(err)
	}
}

// edit replaces old with new exactly once; a change that did not happen
// would let a case pass for the wrong reason.
func edit(t *testing.T, dir, rel, old, new string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), old); n != 1 {
		t.Fatalf("%s: %q occurs %d times, want once", rel, old, n)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(string(b), old, new, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendTo(t *testing.T, dir, rel, more string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, rel, string(b)+more, 0o644)
}

// The acceptance of #34 first, then the other things the issue lists a
// propose-tier hand could change through a patch, then changes the dial may
// keep deciding. The commands and paths are harmless on purpose: what is
// judged is the kind of change, never the command in it.
func TestReviewNamesWhatOnlyAHumanMayAllow(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, dir string)
		human  string // a phrase the reasons must contain; "" = no reason, the dial decides
	}{
		{"an executable post-deploy", func(t *testing.T, d string) {
			write(t, d, "services/whoami/post-deploy", "#!/bin/sh\necho eingerichtet\n", 0o755)
		}, "führt ein Skript direkt auf der Box aus"},
		{"a new host unit", func(t *testing.T, d string) {
			write(t, d, "services/whoami/whoami-helper.service", "[Service]\nExecStart=/usr/bin/true\n\n[Install]\nWantedBy=default.target\n", 0o644)
		}, "läuft direkt auf der Box"},
		{"the host OS image (#368)", func(t *testing.T, d string) {
			write(t, d, "host/image", "# tag: stable-nvidia\nostree-unverified-registry:ghcr.io/ublue-os/ucore-minimal@sha256:b111\n", 0o644)
		}, "Betriebssystem der Box"},
		{"a skill for the Operator", func(t *testing.T, d string) {
			write(t, d, "skills/gartenblick.yml", "description: x\n", 0o644)
		}, "gibt dem Operator eine Fähigkeit"},
		{"policy.yml", func(t *testing.T, d string) {
			edit(t, d, "policy.yml", "pi: {ceiling: propose}", "pi: {ceiling: apply}")
		}, "Freigaberegeln"},
		{"a public route", func(t *testing.T, d string) {
			edit(t, d, "services/whoami/laib.yml", "exposure: internal", "exposure: public")
		}, "aus dem Internet erreichbar"},
		{"box.yml roles", func(t *testing.T, d string) {
			edit(t, d, "box.yml", "  proxy: caddy\n", "  proxy: whoami\n")
		}, "Rollen"},

		{"a secret that mints a Laib token", func(t *testing.T, d string) {
			edit(t, d, "services/whoami/laib.yml", "  - name: WHOAMI_TOKEN\n", "  - name: WHOAMI_TOKEN\n  - {name: WHOAMI_LAIB, external: true, mint: ops}\n")
		}, "Laib-Schlüssel"},
		{"an external secret", func(t *testing.T, d string) {
			edit(t, d, "services/whoami/laib.yml", "  - name: WHOAMI_TOKEN\n", "  - name: WHOAMI_TOKEN\n  - {name: GITHUB_PAT, external: true}\n")
		}, "Geheimnis von außen"},
		{"a host folder as a volume", func(t *testing.T, d string) {
			edit(t, d, "services/whoami/whoami.container", "AutoUpdate=registry\n", "AutoUpdate=registry\nVolume=/srv:/srv\n")
		}, "Zugriff auf den Ordner /srv"},
		{"a value that moves every mount of a service", func(t *testing.T, d string) {
			appendTo(t, d, "services/auth/values.yml", "DATA_DIR: /srv/elsewhere\n")
		}, "Zugriff auf den Ordner /srv/elsewhere/auth"},
		{"an image from someone else", func(t *testing.T, d string) {
			edit(t, d, "services/whoami/values.yml", "docker.io/traefik/whoami:v1.10", "docker.io/someone/whoami:v1.10")
		}, "anderes Programm"},
		{"a port opened to the LAN", func(t *testing.T, d string) {
			edit(t, d, "services/whoami/laib.yml", "{port: 8080, lan: false}", "{port: 8080, lan: true}")
		}, "Heimnetz"},
		{"a published port", func(t *testing.T, d string) {
			edit(t, d, "services/whoami/whoami.container", "PublishPort=127.0.0.1:8080:80", "PublishPort=0.0.0.0:8080:80")
		}, "öffnet den Port"},
		{"a host command inside a container unit", func(t *testing.T, d string) {
			edit(t, d, "services/whoami/whoami.container", "Restart=always\n", "Restart=always\nExecStartPre=/usr/bin/true\n")
		}, "führt einen Befehl direkt auf der Box aus"},
		{"podman arguments", func(t *testing.T, d string) {
			edit(t, d, "services/whoami/whoami.container", "AutoUpdate=registry\n", "AutoUpdate=registry\nPodmanArgs=--log-level=debug\n")
		}, "Sonderrechte"},
		{"another service's secret", func(t *testing.T, d string) {
			edit(t, d, "services/whoami/values.yml", "TOKEN: !secret WHOAMI_TOKEN", "TOKEN: !secret LLDAP_ADMIN_PASSWORD")
		}, "Geheimnis LLDAP_ADMIN_PASSWORD"},
		{"a weaker login", func(t *testing.T, d string) {
			edit(t, d, "services/whoami/laib.yml", "auth: one_factor", "auth: none")
		}, "Anmeldung"},
		{"a path without login", func(t *testing.T, d string) {
			edit(t, d, "services/whoami/laib.yml", "group: family\n", "group: family\n    bypass: [/api/]\n")
		}, "ohne Anmeldung"},
		{"raw caddy config", func(t *testing.T, d string) {
			edit(t, d, "services/whoami/laib.yml", "max_size 10MB", "max_size 20MB")
		}, "Caddy"},
		{"host network", func(t *testing.T, d string) {
			edit(t, d, "services/whoami/laib.yml", "provides: [demo]\n", "provides: [demo]\nhostNetwork: true\n")
		}, "Netz der Box"},
		{"data taken out of the backup", func(t *testing.T, d string) {
			edit(t, d, "services/auth/laib.yml", "  - path: ${DATA_DIR}/auth/lldap\n", "  - path: ${DATA_DIR}/auth/lldap\n    backup: false\n")
		}, "Sicherung"},
		{"the encrypted secrets", func(t *testing.T, d string) {
			write(t, d, "secrets.age", "changed\n", 0o644)
		}, "Geheimnisse"},
		{"what Laib ignores", func(t *testing.T, d string) {
			appendTo(t, d, "ignore.yml", "  - whoami.service\n")
		}, "nicht überwacht"},
		{"a link", func(t *testing.T, d string) {
			if err := os.Symlink("/etc/hostname", filepath.Join(d, "services/whoami/presets.json")); err != nil {
				t.Fatal(err)
			}
		}, "Verweis"},
		{"a file Laib does not know", func(t *testing.T, d string) {
			write(t, d, "services/whoami/notes.txt", "hallo\n", 0o644)
		}, "Datei, die Laib nicht kennt"},
		{"an executable base copy", func(t *testing.T, d string) {
			write(t, d, "services/whoami/.template/post-deploy", "#!/bin/sh\n", 0o755)
		}, "Datei, die Laib nicht kennt"},
		{"a file outside the services", func(t *testing.T, d string) {
			write(t, d, ".gitignore", "services/\n", 0o644)
		}, "außerhalb der Dienste"},
		{"a removed service", func(t *testing.T, d string) {
			os.RemoveAll(filepath.Join(d, "services/whoami"))
		}, "entfernt den Dienst whoami"},

		{"nothing", func(t *testing.T, d string) {}, ""},
		{"a newer image tag", func(t *testing.T, d string) {
			edit(t, d, "services/whoami/values.yml", "whoami:v1.10", "whoami:v1.11")
		}, ""},
		{"an environment value", func(t *testing.T, d string) {
			edit(t, d, "services/whoami/whoami.container", "WHOAMI_NAME=${SERVICE}", "WHOAMI_NAME=hello-${SERVICE}")
		}, ""},
		{"a new generated secret", func(t *testing.T, d string) {
			edit(t, d, "services/whoami/laib.yml", "  - name: WHOAMI_TOKEN\n", "  - name: WHOAMI_TOKEN\n  - name: WHOAMI_SALT\n")
		}, ""},
		{"the template's base copy (#511)", func(t *testing.T, d string) {
			write(t, d, "services/whoami/.template/post-deploy", "#!/bin/sh\necho eingerichtet\n", 0o644)
		}, ""},
		{"a README", func(t *testing.T, d string) {
			write(t, d, "services/whoami/README.md", "# whoami\n", 0o644)
		}, ""},
		{"a health URL", func(t *testing.T, d string) {
			edit(t, d, "services/whoami/laib.yml", "http: http://127.0.0.1:8080/", "http: http://127.0.0.1:8080/health")
		}, ""},
		{"a restriction added", func(t *testing.T, d string) {
			edit(t, d, "services/caddy/caddy.container", "[Container]\n", "[Container]\nReadOnly=true\n")
		}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base, changed := copyTree(t, golden), copyTree(t, golden)
			c.change(t, changed)
			r := Dirs(base, changed)
			got := strings.Join(r.Human, " | ")
			switch {
			case c.human == "" && len(r.Human) > 0:
				t.Errorf("the dial should decide, got: %s", got)
			case c.human != "" && !strings.Contains(got, c.human):
				t.Errorf("want a reason with %q, got: %q", c.human, got)
			}
		})
	}
}

// Taking a restriction away loosens the container: a human's call. Adding
// one is fine (the last case above).
func TestReviewSeesARestrictionTakenAway(t *testing.T) {
	base, changed := copyTree(t, golden), copyTree(t, golden)
	edit(t, base, "services/caddy/caddy.container", "[Container]\n", "[Container]\nReadOnly=true\n")
	r := Dirs(base, changed)
	if !strings.Contains(strings.Join(r.Human, " "), "ReadOnly") {
		t.Fatalf("removing ReadOnly=true went unnoticed: %q", r.Human)
	}
}

// A new service is the new-service class, not a patch; the hands its secrets
// mint tokens for are named, so the caller can refuse a token stronger than
// the hand that asked for it.
func TestReviewNamesNewServicesAndMints(t *testing.T) {
	unit := "[Container]\nImage=docker.io/library/busybox:1.36\nExec=sleep infinity\n\n[Install]\nWantedBy=default.target\n"
	base, changed := copyTree(t, golden), copyTree(t, golden)
	write(t, changed, "services/clock/laib.yml", "name: clock\nsecrets:\n  - {name: CLOCK_LAIB, external: true, mint: bote}\n", 0o644)
	write(t, changed, "services/clock/clock.container", unit, 0o644)
	r := Dirs(base, changed)
	if !reflect.DeepEqual(r.Added, []string{"clock"}) {
		t.Errorf("added: %v", r.Added)
	}
	if !reflect.DeepEqual(r.Mints, []string{"bote"}) {
		t.Errorf("mints: %v", r.Mints)
	}
	quiet := copyTree(t, golden)
	write(t, quiet, "services/clock/laib.yml", "name: clock\n", 0o644)
	write(t, quiet, "services/clock/clock.container", unit, 0o644)
	if r := Dirs(base, quiet); len(r.Human) > 0 || !reflect.DeepEqual(r.Added, []string{"clock"}) {
		t.Errorf("a quiet new service: human %q, added %v", r.Human, r.Added)
	}
}

// A state that does not load cannot be judged: that is a human's call too.
func TestReviewFailsClosed(t *testing.T) {
	base, changed := copyTree(t, golden), copyTree(t, golden)
	write(t, changed, "services/whoami/laib.yml", "routes: [", 0o644)
	if r := Dirs(base, changed); len(r.Human) == 0 {
		t.Fatal("a state that does not load was not marked for a human")
	}
}

// #58: beside the human-only reasons, the review states plain facts for
// the decision card — where a new service is reachable.
func TestReviewStatesWhereANewServiceIsReachable(t *testing.T) {
	base, changed := copyTree(t, golden), copyTree(t, golden)
	write(t, changed, "services/paperless/laib.yml", "name: paperless\nroutes:\n  - host: paperless\n    port: 8000\n    exposure: internal\n    auth: one_factor\nsecrets: []\ndata: []\n", 0o644)
	write(t, changed, "services/paperless/paperless.container", "[Container]\nImage=docker.io/library/busybox:1.36\n\n[Install]\nWantedBy=default.target\n", 0o644)
	r := Dirs(base, changed)
	joined := strings.Join(r.Facts, "\n")
	if !strings.Contains(joined, "paperless.") || !strings.Contains(joined, "nur im Heimnetz") {
		t.Fatalf("facts must say where the new service is reachable:\n%s", joined)
	}
}

// #616: the review records what a change grants and sets, before → after,
// so the card can say it in words: a hand's verbs and path grants with what
// it keeps, and a service's values.
func TestReviewRecordsWhatChangesBeforeAndAfter(t *testing.T) {
	base, changed := copyTree(t, golden), copyTree(t, golden)
	edit(t, base, "policy.yml", "  pi: {ceiling: propose}\n", "  pi: {ceiling: propose}\n  haus: {ceiling: apply, verbs: [status, restart]}\n")
	edit(t, changed, "policy.yml", "  pi: {ceiling: propose}\n", "  pi: {ceiling: propose}\n  haus: {ceiling: apply, verbs: [status, zuhause], paths: [people.*.signal]}\n")
	edit(t, changed, "services/whoami/values.yml", "IMAGE: docker.io/traefik/whoami:v1.10", "IMAGE: docker.io/traefik/whoami:v1.11")
	r := Dirs(base, changed)
	var got []string
	for _, c := range r.Changes {
		got = append(got, c.Op+" "+c.Hand+c.Service+" "+c.Verb+c.Path+c.Key+" "+c.Old+">"+c.New)
	}
	want := []string{"+ haus zuhause >", "+ haus people.*.signal >", "- haus restart >", "= haus status >", "~ whoami IMAGE docker.io/traefik/whoami:v1.10>docker.io/traefik/whoami:v1.11"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("changes:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
