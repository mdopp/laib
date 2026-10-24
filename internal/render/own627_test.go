package render

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// ADR 0027 (#624): laib-operator and laib-notify are no host units any
// more. Each is a Quadlet container from an image the release pins, with
// only its own folder mounted and its secrets as podman secrets: the age
// key, secrets.age, the state repo and the journal stay out of reach.
func TestOperatorAndNotifyRunInContainersThatSeeOnlyTheirOwnFolder(t *testing.T) {
	st, err := state.Load("../../testdata/golden/nuc/state")
	if err != nil {
		t.Fatal(err)
	}
	st.Box.Watchdog.URL = "https://hc.example/ping/abc"
	sec := map[string]string{OperatorToken: "op-secret-1", NotifyToken: "notify-secret-2", "USAGE_METRICS_TOKEN": "usage-secret-3", "AUTHELIA_SMTP_PASSWORD": "smtp-secret-4"}
	out, err := Render(st, sec)
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"systemd/laib-operator.service", "systemd/laib-operator.env", "systemd/laib-notify.service", "systemd/laib-notify.env"} {
		if _, ok := out[gone]; ok {
			t.Errorf("%s still rendered: the program runs in a container now", gone)
		}
	}
	if !strings.Contains(out["systemd/laib-notify.timer"], "OnUnitActiveSec=5m") {
		t.Error("the notify timer is gone: it starts the container's unit every five minutes")
	}
	home := "/mnt/data/laib"
	never := []string{filepath.Join(home, "age.key"), filepath.Join(home, "state", "secrets.age"), filepath.Join(home, "state"), filepath.Join(home, "journal")}
	cases := map[string]struct{ image, folder, inside string }{
		"laib-operator": {OperatorImage, home + "/operator", "/operator"},
		"laib-notify":   {NotifyImage, home + "/notify", "/notify"},
	}
	podSec := PodmanSecrets(st, sec)
	for name, c := range cases {
		unit := out["quadlet/"+name+"/"+name+".container"]
		if unit == "" {
			t.Fatalf("%s: no container rendered", name)
		}
		for _, w := range []string{"\nImage=" + c.image + "\n", "\nVolume=" + c.folder + ":" + c.inside + ":Z\n", "\nNetwork=host\n", "\nReadOnly=true\n", "\nNoNewPrivileges=true\n", "\nDropCapability=all\n", "\nExecStartPre=/usr/bin/mkdir -p " + c.folder + "\n"} {
			if !strings.Contains(unit, w) {
				t.Errorf("%s lacks %q:\n%s", name, strings.TrimSpace(w), unit)
			}
		}
		// the one mount: no other Volume=, Mount=, no env file, no podman args
		mounts := 0
		for _, line := range strings.Split(unit, "\n") {
			k, v, _ := strings.Cut(line, "=")
			switch k {
			case "Volume", "Mount":
				mounts++
				src := strings.Split(v, ":")[0]
				for _, p := range never {
					if p == src || strings.HasPrefix(p, src+"/") {
						t.Errorf("%s mounts %s, which holds %s", name, src, p)
					}
				}
			case "EnvironmentFile", "PodmanArgs", "AddCapability", "SecurityLabelDisable", "Rootfs":
				t.Errorf("%s carries %s", name, line)
			case "Environment":
				for _, p := range never {
					if strings.Contains(v, p) {
						t.Errorf("%s names %s in its environment: %s", name, p, line)
					}
				}
			}
		}
		if mounts != 1 {
			t.Errorf("%s has %d mounts, want its own folder only:\n%s", name, mounts, unit)
		}
		for _, v := range sec {
			if strings.Contains(unit, v) {
				t.Errorf("%s carries a secret value in the unit", name)
			}
		}
		tok := "laib." + name + "." + name + ".LAIB_TOKEN"
		if !strings.Contains(unit, "\nSecret="+tok+",type=env,target=LAIB_TOKEN\n") {
			t.Errorf("%s: the token is no podman secret:\n%s", name, unit)
		}
		if want := map[string]string{"laib-operator": "op-secret-1", "laib-notify": "notify-secret-2"}[name]; podSec[tok] != want {
			t.Errorf("%s: podman secret %s = %q, want %q", name, tok, podSec[tok], want)
		}
	}
	op := out["quadlet/laib-operator/laib-operator.container"]
	for _, w := range []string{`Environment="OPERATOR_HOME=/operator"`, `Environment="LAIB_MCP_URL=http://127.0.0.1:5900/mcp"`, `Environment="MODEL_URL=http://192.168.1.20:11435/v1"`, `Environment="OPERATOR_LISTEN=127.0.0.1:5901"`, "Restart=always\n", "WantedBy=default.target\n"} {
		if !strings.Contains(op, w) {
			t.Errorf("the operator lacks %q:\n%s", strings.TrimSpace(w), op)
		}
	}
	nt := out["quadlet/laib-notify/laib-notify.container"]
	for _, w := range []string{`Environment="NOTIFY_HOME=/notify"`, "Type=oneshot\n", "ExecStartPre=-/usr/bin/mv -n /mnt/data/laib/mail.json /mnt/data/laib/watchdog.last /mnt/data/laib/notify/\n"} {
		if !strings.Contains(nt, w) {
			t.Errorf("notify lacks %q:\n%s", strings.TrimSpace(w), nt)
		}
	}
	if strings.Contains(nt, "[Install]") {
		t.Errorf("notify runs from its timer, not at boot:\n%s", nt)
	}
	// without the secrets (lint, review) the units name the placeholders' secrets all the same
	if lint, _ := Render(st, nil); !strings.Contains(lint["quadlet/laib-operator/laib-operator.container"], "Secret=laib.laib-operator.laib-operator.LAIB_TOKEN,type=env,target=LAIB_TOKEN") {
		t.Errorf("lint render lost the operator's secret line")
	}
}

// An empty secret (no model key) stays out of the unit and out of podman:
// podman refuses an empty secret (#224), and unset is what the program reads.
func TestOwnContainerLeavesAnEmptySecretOut(t *testing.T) {
	st, err := state.Load("../../testdata/golden/nuc/state")
	if err != nil {
		t.Fatal(err)
	}
	st.Box.Model.KeySecret = "MODEL_API_KEY"
	out, _ := Render(st, map[string]string{OperatorToken: "op"})
	if op := out["quadlet/laib-operator/laib-operator.container"]; strings.Contains(op, "MODEL_KEY") {
		t.Errorf("an empty model key is left out, neither a secret nor a line:\n%s", op)
	}
	if _, ok := PodmanSecrets(st, map[string]string{OperatorToken: "op"})["laib.laib-operator.laib-operator.MODEL_KEY"]; ok {
		t.Error("an empty podman secret")
	}
}
