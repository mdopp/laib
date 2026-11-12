package render

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/state"
)

// #1043: the status page at the box's bare LAN address.

func lanState(t *testing.T) *state.State {
	t.Helper()
	st, err := state.Load("../../testdata/golden/nuc/state")
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// lanRows: every service of the state green, the named one in its state
func lanRows(st *state.State, name, state string) []LanRow {
	var rows []LanRow
	for _, n := range st.ServiceNames() {
		r := LanRow{Service: n, State: "grün"}
		if n == name {
			r.State = state
		}
		rows = append(rows, r)
	}
	return rows
}

// dienst: a service's section of the page
func dienst(page, title string) string {
	for _, s := range strings.Split(page, "<section class=\"dienst ")[1:] {
		if strings.Contains(s, "<b>"+title+"</b>") {
			return s
		}
	}
	return ""
}

func TestLanPageShowsEveryServiceAndCanGoRed(t *testing.T) {
	st := lanState(t)
	at := time.Date(2026, 11, 4, 7, 14, 0, 0, time.UTC)
	green := LanPage(st, &LanStatus{At: at, Version: "2.1.0", Uptime: 75 * time.Hour, Rows: lanRows(st, "", "")})
	for _, n := range st.ServiceNames() {
		title := cmp.Or(st.Services[n].Contract.Title, n)
		if s := dienst(green, title); !strings.HasPrefix(s, "grün\">") {
			t.Errorf("%s (%s) is not on the page as grün: %.80q", n, title, s)
		}
	}
	if !strings.Contains(green, "<b>Alles in Ordnung</b>") || strings.Contains(green, "dienst rot") {
		t.Error("all green: the box says Alles in Ordnung and no service is red")
	}
	// the same box with one service red: its line says rot, first, and the box says so
	red := LanPage(st, &LanStatus{At: at, Rows: lanRows(st, "auth", "rot")})
	title := cmp.Or(st.Services["auth"].Contract.Title, "auth")
	s := dienst(red, title)
	if !strings.HasPrefix(s, "rot\">") || !strings.Contains(s, "<span class=dot></span>rot</span>") {
		t.Errorf("a red service is not shown as rot: %.200q", s)
	}
	if !strings.Contains(red, "<section class=\"box rot\"><p class=verdict><span class=dot></span><b>1 Dienst gestört</b>") {
		t.Error("one red service: the box line is not red with „1 Dienst gestört“")
	}
	if i := strings.Index(red, "<section class=\"dienst "); !strings.HasPrefix(red[i:], "<section class=\"dienst rot\"><p><b>"+title) {
		t.Error("the red service is not the first line")
	}
	// a problem beyond the services is red too
	if p := LanPage(st, &LanStatus{At: at, Problems: 2, Rows: lanRows(st, "", "")}); !strings.Contains(p, "class=\"box rot\"") || !strings.Contains(p, "2 Probleme") {
		t.Error("two problems of the box (disk, backup) leave the box line green")
	}
}

func TestLanPageLinksTheNamesTheLANPortsAndVerwalten(t *testing.T) {
	st := lanState(t)
	// as Jellyfin's 8096 on the box: the route's port published to the LAN; and a port without a route, as MQTT's
	st.Services["whoami"].Contract.Ports[0].LAN = true
	st.Services["adguard"].Contract.Ports = append(st.Services["adguard"].Contract.Ports, state.Port{Port: 1883, LAN: true})
	p := LanPage(st, nil)
	if !strings.Contains(dienst(p, "adguard"), "<span class=muted>mqtt://192.168.1.10:1883</span>") {
		t.Error("a LAN port without a route is not named by the address")
	}
	if !strings.Contains(p, `<a class=verwalten href="https://laib.nuc.home/">Verwalten</a>`) {
		t.Error("no link to the Laib page")
	}
	// a route's name; and its port when the LAN reaches it, by number, no name needed
	for _, n := range Net(st).Names {
		if !strings.HasPrefix(n.Upstream, "→") && n.Name != "laib.nuc.home" && !strings.Contains(p, `href="https://`+n.Name+`/"`) {
			t.Errorf("%s is not linked", n.Name)
		}
	}
	lan := 0
	for _, port := range Net(st).Ports {
		web := false
		if port.Service == "laib" {
			continue // Laib's own port, never the LAN's
		}
		for _, r := range st.Services[port.Service].Contract.Routes {
			web = web || r.Port == port.Port
		}
		link := strings.Contains(p, `href="http://192.168.1.10:`+strconv.Itoa(port.Port)+`/"`)
		switch {
		case port.Reach == "LAN" && web:
			lan++
			if !link {
				t.Errorf("%s's port %d reaches the LAN and serves its route, not linked by number", port.Service, port.Port)
			}
		case link:
			t.Errorf("%s's port %d is linked by number: %s, no route", port.Service, port.Port, port.Reach)
		}
	}
	if lan == 0 {
		t.Fatal("the nuc case has no route port on the LAN: this test proves nothing")
	}
	if strings.Contains(p, "<script src") || strings.Contains(p, "<link") || regexp.MustCompile(`(src|href)="//`).MatchString(p) {
		t.Error("the page loads something: it must stand alone, also without names")
	}
}

// runLanJS runs the page's own script in node against a minimal DOM: the
// body's data, #frisch and #alt, the reader's clock at age past the Stand.
// It returns what the reader sees: the Stand line, the banner (or ""), and
// whether the services are dimmed.
func runLanJS(t *testing.T, page string, age time.Duration) (stand, banner string, dimmed bool) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("node is missing in CI: the page's freshness is not measured")
		}
		t.Skip("no node: the page's freshness is measured in CI")
	}
	_, js, _ := strings.Cut(page, "<script>")
	js, _, _ = strings.Cut(js, "</script>")
	data := map[string]string{}
	for _, k := range []string{"stand", "hm", "hms"} {
		_, v, _ := strings.Cut(page, " data-"+k+"=\"")
		data[k], _, _ = strings.Cut(v, "\"")
	}
	stub := fmt.Sprintf(`var cls={},body={dataset:%s,classList:{toggle:function(c,on){cls[c]=on}}};
var el={frisch:{className:'stand',firstChild:{textContent:''}},alt:{hidden:true,textContent:''}};
var document={body:body,getElementById:function(id){return el[id]}};
var setInterval=function(){};Date.now=function(){return Date.parse(body.dataset.stand)+%d};
%s
console.log(JSON.stringify({stand:el.frisch.firstChild.textContent,live:el.frisch.className,banner:el.alt.hidden?'':el.alt.textContent,dimmed:!!cls.veraltet}))`, mustJSON(data), age.Milliseconds(), js)
	out, err := exec.Command(node, "-e", stub).CombinedOutput()
	if err != nil {
		t.Fatalf("the page's script fails: %v\n%s", err, out)
	}
	var got struct {
		Stand, Live, Banner string
		Dimmed              bool
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if (got.Live == "stand live") != strings.HasPrefix(got.Stand, "Live") {
		t.Errorf("the Stand line says %q with class %q", got.Stand, got.Live)
	}
	return got.Stand, got.Banner, got.Dimmed
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestLanPageSaysLiveOnlyWhileItsStandIsFresh(t *testing.T) {
	st := lanState(t)
	at := time.Date(2026, 11, 4, 8, 14, 5, 0, time.Local)
	p := LanPage(st, &LanStatus{At: at, Rows: lanRows(st, "auth", "rot")})
	// without JavaScript: the absolute time, prominent, and no claim of live
	if !strings.Contains(p, "<p id=frisch class=stand><b>Stand 04.11. 08:14:05</b></p>") || strings.Contains(p, "Live") && !strings.Contains(p, "'Live · Stand '") {
		t.Error("without JavaScript the page must show its Stand as a time and never say live")
	}
	// fresh: written 40 seconds ago
	stand, banner, dimmed := runLanJS(t, p, 40*time.Second)
	if stand != "Live · Stand 04.11. 08:14:05" || banner != "" || dimmed {
		t.Errorf("a fresh page: want live, no banner, not dimmed; got %q, %q, %v", stand, banner, dimmed)
	}
	// old: laib stopped, the file is twelve minutes old
	stand, banner, dimmed = runLanJS(t, p, 12*time.Minute+10*time.Second)
	if strings.HasPrefix(stand, "Live") || !strings.HasPrefix(banner, "Alter Stand von 08:14 – Laib antwortet seit 12 Minuten nicht") || !dimmed {
		t.Errorf("a stale page: want the old-Stand banner and the services dimmed; got %q, %q, %v", stand, banner, dimmed)
	}
	if !strings.Contains(p, `.veraltet .dienst .state:before{content:"zuletzt bekannt: "`) {
		t.Error("a stale page must mark the states as last known")
	}
	// the edge: the write interval plus margin is still live, past it is not
	if s, _, _ := runLanJS(t, p, 2*time.Minute); !strings.HasPrefix(s, "Live") {
		t.Errorf("two minutes old (a write was due a minute ago) is still live: %q", s)
	}
	if s, b, _ := runLanJS(t, p, 3*time.Minute); strings.HasPrefix(s, "Live") || b == "" {
		t.Errorf("three minutes old is not live: %q", s)
	}
	// before the first status: no state is guessed, and the page says why
	f := LanPage(st, nil)
	if !strings.Contains(f, "<b>Noch kein Stand</b>") || strings.Contains(f, "grün\">") || strings.Contains(f, "id=frisch") {
		t.Error("the fallback must say there is no Stand yet and show no state")
	}
}

// secretShaped: what a token, key or password looks like on a page
var secretShaped = regexp.MustCompile(`\$\{secret:|sb_[A-Za-z0-9]|-----BEGIN|[A-Za-z0-9+/_-]{32,}`)

func TestLanPageCarriesNoSecret(t *testing.T) {
	st := lanState(t)
	sec := map[string]string{}
	for _, s := range SecretNames(st) {
		sec[s.Name] = "Geheim" + strings.Repeat("x9", 20) + s.Name
	}
	sec[ProxySecret] = "Geheim-proxy-0123456789abcdef0123456789abcdef"
	out, err := Render(st, sec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out["caddy/Caddyfile"], sec[ProxySecret]) {
		t.Fatal("the Caddyfile carries no secret: the test would not see one leak")
	}
	live := LanPage(st, &LanStatus{At: time.Now(), Version: "2.1.0", Uptime: time.Hour, Rows: lanRows(st, "auth", "rot")})
	for name, page := range map[string]string{"fallback": out["status/fallback.html"], "live": live} {
		if page == "" {
			t.Fatalf("%s: no page", name)
		}
		for k, v := range sec {
			if strings.Contains(page, v) {
				t.Errorf("%s page carries the secret %s", name, k)
			}
		}
		if m := secretShaped.FindString(page); m != "" {
			t.Errorf("%s page has a secret-shaped value: %q", name, m)
		}
	}
	// the shape check sees a token
	if secretShaped.FindString(LanPage(st, nil)+"sb_0123abcd") == "" || secretShaped.FindString("a0b1c2d3e4f5a0b1c2d3e4f5a0b1c2d3e4f5") == "" {
		t.Error("the secret shape matches no token: it cannot go red")
	}
}

// lanSite: the bare address's site in the Caddyfile
func siteOf(caddyfile, ip string) string {
	_, site, _ := strings.Cut(caddyfile, "\nhttp://"+ip+" {\n")
	site, _, _ = strings.Cut(site, "\n}\n")
	return site
}

// lanOnly: the page is served only inside the remote_ip handle, everyone
// else gets the lanonly denial. Caddy's own refusal of a public address is
// measured in CI (ci.yml), against the real image.
func lanOnly(site string) bool {
	inner, rest, ok := strings.Cut(site, "\t@lan remote_ip private_ranges\n\thandle @lan {\n")
	if !ok || strings.TrimSpace(inner) != "" {
		return false
	}
	handle, after, ok := strings.Cut(rest, "\n\t}\n")
	return ok && strings.Contains(handle, "file_server") && strings.TrimSpace(after) == "import lanonly"
}

// Ablauf L-36
func TestLanSiteServesTheLANOnly(t *testing.T) {
	st := lanState(t)
	cf := caddyfile(st, nil)
	site := siteOf(cf, "192.168.1.10")
	if !lanOnly(site) {
		t.Errorf("the bare address's site is not LAN only:\n%s", site)
	}
	if strings.Contains(cf, "https://192.168.1.10") || strings.Contains(cf, "\n192.168.1.10") {
		t.Error("the bare address has a TLS site: no certificate for it is valid, so nothing is on 443")
	}
	// the check sees a page outside the handle
	open := strings.Replace(site, "\timport lanonly", "\tfile_server", 1)
	if lanOnly(open) {
		t.Error("lanOnly passes a site that serves outside the LAN: it cannot go red")
	}
	// no lanIp, no site and no page
	st.Box.LanIP = ""
	out, _ := Render(st, nil)
	if strings.Contains(out["caddy/Caddyfile"], "\nhttp://") || out["status/fallback.html"] != "" {
		t.Error("without box.yml lanIp there is no bare address to serve")
	}
}

// The owner, 2026-11-04: „bitte auch immer die lokale Adresse (also IP:Port)
// darstellen, so dass ich es auch ohne DNS-Auflösung öffnen kann.“ A route
// whose port the LAN reaches gets its http://<lanIp>:<port>/ link; one only
// Caddy reaches by its name says so, never a link that would not open; a
// port that speaks no web is text; a service without either says that too.
func TestLanPageGivesEachServiceItsLANAddressOrSaysThereIsNone(t *testing.T) {
	st := lanState(t)
	title := func(n string) string { return cmp.Or(st.Services[n].Contract.Title, n) }
	st.Services["whoami"].Contract.Ports[0].LAN = true // published to the LAN, as Jellyfin's 8096
	// AdGuard in the host network with its route's port undeclared, as Home Assistant's 8123: nftables lets it through
	st.Services["adguard"].Contract.Ports = nil
	// auth: routes, its ports dropped on the LAN: only Caddy reaches it, by name
	// usage-metrics: a Samba port, no route
	st.Services["usage-metrics"].Contract.Ports = []state.Port{{Port: 445, LAN: true}, {Port: 139, LAN: true}}
	for _, page := range []string{LanPage(st, nil), LanPage(st, &LanStatus{At: time.Now(), Rows: lanRows(st, "", "")})} {
		for _, c := range []struct{ svc, want string }{
			{"whoami", `<a href="http://192.168.1.10:8080/">192.168.1.10:8080</a>`},
			{"adguard", `<a href="http://192.168.1.10:8083/">192.168.1.10:8083</a>`},
			{"auth", "<span class=muted>nur über den Namen erreichbar</span>"},
			{"usage-metrics", "<span class=muted>smb://192.168.1.10</span></p>"},
			{"frage", "<span class=muted>keine Adresse zum Öffnen</span>"},
		} {
			s := dienst(page, title(c.svc))
			if !strings.Contains(s, c.want) {
				t.Errorf("%s: want %s, the line is %.400q", c.svc, c.want, s)
			}
			if c.svc != "whoami" && c.svc != "adguard" && strings.Contains(s, "http://192.168.1.10") {
				t.Errorf("%s: linked by the address, the LAN does not reach its web port: %.400q", c.svc, s)
			}
		}
		if s := dienst(page, title("usage-metrics")); strings.Count(s, "smb://") != 1 {
			t.Errorf("Samba's two ports are one address: %.300q", s)
		}
	}
	for _, p := range Net(st).Ports {
		if p.Service == "adguard" && p.Port == 8083 && p.Reach != "LAN" {
			t.Errorf("the host network's undeclared route port is %q on the map, nftables lets it through", p.Reach)
		}
	}
}
