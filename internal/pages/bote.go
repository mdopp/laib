package pages

import (
	"fmt"
	"html"
	"slices"
	"strings"

	"github.com/mdopp/laib/internal/state"
)

// BoteLinkPage tells how the Bote gets its Signal number (#469). signal-cli
// runs as its own daemon, which has no QR endpoint: the number is either
// registered as the Bote's own (a spare landline by voice call, no second
// SIM, the Kraftwerk's way) or linked to a phone that has it. Both run the
// image's signal-cli once, with the daemon stopped.
func BoteLinkPage(dataDir, account string) string {
	run := "podman run --rm -it -v " + dataDir + "/bote/signal:/home/.local/share/signal-cli:z --entrypoint /usr/bin/signal-cli-native docker.io/bbernhard/signal-cli-rest-api --config /home/.local/share/signal-cli -a " + account
	return html.EscapeString(`Auf der Box, als core:
systemctl --user stop bote-signal

Eigene Nummer (z. B. ein Festnetz-Anschluss, ohne zweite SIM):
` + run + ` register --captcha 'signalcaptcha://…'      (Captcha: signalcaptchas.org/registration/generate.html; erst SMS)
` + run + ` register --voice --captcha 'signalcaptcha://…'   (nach einer Minute: Anruf)
` + run + ` verify <Code>
` + run + ` setPin <PIN>      (Registrierungssperre)

Oder als Gerät eines Telefons mit dieser Nummer:
` + strings.Replace(run, " -a "+account, "", 1) + ` link -n Bote      (den sgnl://-Link als QR in Signal → Gekoppelte Geräte scannen)

podman unshare chown -R 1000:1000 ` + dataDir + `/bote/signal
systemctl --user start bote-signal`)
}

// boteAccount is the Bote's own Signal number, from the service providing `bote`.
func boteAccount(st *state.State) string {
	for _, name := range st.ServiceNames() {
		if svc := st.Services[name]; slices.Contains(svc.Contract.Provides, "bote") {
			return svc.Values[svc.Contract.Channels["signal"]].Literal
		}
	}
	return ""
}

// BoteSignalBase is signal-cli's REST address on this box: the first port
// the service providing `bote` declares (its laib.yml says so), on loopback;
// empty while the Bote has no Signal (#184).
func BoteSignalBase(st *state.State) string {
	for _, name := range st.ServiceNames() {
		svc := st.Services[name]
		for _, p := range svc.Contract.Provides {
			if p == "bote" && len(svc.Contract.Ports) > 0 && svc.Set(svc.Contract.Channels["signal"]) {
				return fmt.Sprintf("http://127.0.0.1:%d", svc.Contract.Ports[0].Port)
			}
		}
	}
	return ""
}
