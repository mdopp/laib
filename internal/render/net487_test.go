package render

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
	"gopkg.in/yaml.v3"
)

// #487: `laib net` showed admin. as one_factor while Authelia got two_factor,
// and Laib's own port and Samba's and Syncthing's were missing from the port
// list. The map is checked here against what the box loads, the rendered
// Authelia rules and the nftables drop, for every built-in template, with
// Laib listening for the pods (0.0.0.0) as on the Kraftwerk and on loopback.
func TestTheNetworkMapIsWhatAutheliaAndNftablesGet(t *testing.T) {
	for _, listen := range []string{"0.0.0.0", "127.0.0.1"} {
		st, err := state.Load(filepath.Join("..", "..", "testdata", "golden", "nuc", "state"))
		if err != nil {
			t.Fatal(err)
		}
		st.Box.Laib.Listen = listen
		st.Services = map[string]*state.Service{}
		dirs, _ := filepath.Glob("../../templates/*/laib.yml")
		for _, f := range dirs {
			svc := &state.Service{Name: filepath.Base(filepath.Dir(f))}
			b, _ := os.ReadFile(f)
			if err := yaml.Unmarshal(b, &svc.Contract); err != nil {
				t.Fatal(f, err)
			}
			st.Services[svc.Name] = svc
		}
		n := Net(st)

		// Authelia: domain → policy and subject of its rule without resources
		type rule struct{ policy, subject string }
		rules := map[string]rule{}
		bypass := map[string][]string{}
		var doc struct {
			AccessControl struct {
				Rules []struct {
					Domain    string   `yaml:"domain"`
					Policy    string   `yaml:"policy"`
					Subject   any      `yaml:"subject"`
					Resources []string `yaml:"resources"`
				} `yaml:"rules"`
			} `yaml:"access_control"`
		}
		if err := yaml.Unmarshal([]byte(autheliaAccess(st)), &doc); err != nil {
			t.Fatal(err)
		}
		for _, r := range doc.AccessControl.Rules {
			if len(r.Resources) > 0 {
				bypass[r.Domain] = append(bypass[r.Domain], strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(r.Resources[0], "^"), ".*$"), "/"))
				continue
			}
			var groups []string
			switch s := r.Subject.(type) {
			case string:
				groups = []string{s}
			case []any:
				for _, g := range s {
					groups = append(groups, g.(string))
				}
			}
			for i := range groups {
				groups[i] = strings.TrimPrefix(groups[i], "group:")
			}
			rules[r.Domain] = rule{r.Policy, strings.Join(groups, ", ")}
		}
		admin := false
		for _, nn := range n.Names {
			if strings.HasPrefix(nn.Upstream, "→") {
				continue
			}
			r, ok := rules[nn.Name]
			if !ok {
				t.Errorf("%s: on the map but Authelia has no rule for it", nn.Name)
				continue
			}
			if nn.Policy != r.policy || nn.Subject != r.subject {
				t.Errorf("%s: map says %s %q, Authelia gets %s %q", nn.Name, nn.Policy, nn.Subject, r.policy, r.subject)
			}
			var want []string
			for _, p := range nn.Bypass {
				want = append(want, strings.TrimSuffix(p, "/"))
			}
			if !slices.Equal(want, bypass[nn.Name]) {
				t.Errorf("%s: map bypasses %v, Authelia %v", nn.Name, want, bypass[nn.Name])
			}
			admin = admin || nn.Name == fqdn(st, st.Box.Laib.Host)
		}
		if !admin || rules[fqdn(st, st.Box.Laib.Host)].policy != "two_factor" {
			t.Errorf("Laib's own page: on the map %v, Authelia %+v", admin, rules[fqdn(st, st.Box.Laib.Host)])
		}

		// nftables: a port in the drop set is "nur Box und Pods", every other one LAN
		dropped := map[int]bool{}
		if m := regexp.MustCompile(`tcp dport \{ ([^}]*) \}`).FindStringSubmatch(nftables(st)); m != nil {
			for _, p := range strings.Split(m[1], ", ") {
				port, _ := strconv.Atoi(p)
				dropped[port] = true
			}
		}
		listed := map[int]string{}
		for _, p := range n.Ports {
			listed[p.Port] = p.Service
			want := "LAN"
			switch {
			case dropped[p.Port]:
				want = "nur Box und Pods"
			case p.Service == "laib":
				want = "nur Box" // loopback only
			}
			if p.Reach != want {
				t.Errorf("listen %s: port %d (%s) is %q on the map, nftables makes it %q", listen, p.Port, p.Service, p.Reach, want)
			}
		}
		for port := range dropped {
			if listed[port] == "" {
				t.Errorf("listen %s: nftables drops %d, the map does not list it", listen, port)
			}
		}
		if listed[st.Box.Laib.Port] != "laib" {
			t.Errorf("listen %s: Laib's own port %d is not on the map: %v", listen, st.Box.Laib.Port, listed)
		}
		// Samba and Syncthing run in the host network and reach the LAN (#487)
		for _, port := range []int{139, 445, 22000} {
			if listed[port] != "file-share" {
				t.Errorf("port %d: %q, want file-share on the LAN", port, listed[port])
			}
		}
	}
}
