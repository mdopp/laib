package main

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/policy"
)

// /proc/net/arp as the kernel writes it; documentation addresses, made-up MACs.
const arpFixture = `IP address       HW type     Flags       HW address            Mask     Device
192.0.2.1        0x1         0x2         02:00:5e:10:00:01     *        enp1s0
192.0.2.33       0x1         0x2         00:00:5e:aa:bb:cc     *        enp1s0
192.0.2.40       0x1         0x0         00:00:00:00:00:00     *        enp1s0
192.0.2.41       0x1         0x2         3a:11:22:33:44:55     *        enp1s0
198.51.100.7     0x1         0x2         00:00:5e:00:00:07     *        podman0
`

const ouiFixture = `OUI/MA-L                                                    Organization
company_id                                                  Organization
                                                            Address

00-00-5E   (hex)		Beispiel Drucker GmbH
00005E     (base 16)		Beispiel Drucker GmbH
				Musterweg 1
`

// #577: the parser keeps complete entries of the box's /24, drops the
// incomplete one and the other subnet, and names a maker only from the file.
func TestParseARPKeepsTheLANsAnsweringDevices(t *testing.T) {
	got := parseARP(strings.NewReader(arpFixture), netip.MustParsePrefix("192.0.2.0/24"))
	if len(got) != 3 || got[0].IP != "192.0.2.1" || got[1].MAC != "00:00:5e:aa:bb:cc" || got[2].IP != "192.0.2.41" {
		t.Fatalf("parsed %+v", got)
	}
	m := makers(strings.NewReader(ouiFixture))
	if m["00:00:5e"] != "Beispiel Drucker GmbH" || len(m) != 1 {
		t.Fatalf("makers %v", m)
	}
	if all := parseARP(strings.NewReader(arpFixture), netip.Prefix{}); len(all) != 4 {
		t.Fatalf("without a subnet every complete entry: %+v", all)
	}
}

// #577: the verb over MCP, read tier: devices with name and maker, a random
// MAC said as such, and the limits as a field, not as fine print.
func TestNetGeraeteListsDevicesWithTheirLimits(t *testing.T) {
	env, _ := testBox(t)
	box := filepath.Join(env.Paths.State, "box.yml")
	b, _ := os.ReadFile(box)
	if err := os.WriteFile(box, []byte(strings.Replace(string(b), "10.0.0.2", "192.0.2.10", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	arp, oui := filepath.Join(dir, "arp"), filepath.Join(dir, "oui.txt")
	os.WriteFile(arp, []byte(arpFixture), 0o644)
	os.WriteFile(oui, []byte(ouiFixture), 0o644)
	oldN, oldO, oldL, oldW := neighFile, ouiFile, lookupName, wakeLAN
	t.Cleanup(func() { neighFile, ouiFile, lookupName, wakeLAN = oldN, oldO, oldL, oldW })
	neighFile, ouiFile = arp, oui
	lookupName = func(ip string) string { return map[string]string{"192.0.2.33": "epson-buero.fritz.box"}[ip] }
	woke := ""
	wakeLAN = func(p netip.Prefix, self netip.Addr) { woke = p.String() + " " + self.String() }

	st, err := cli.LoadState(env)
	if err != nil {
		t.Fatal(err)
	}
	out, code := mcp.Call(context.Background(), env, st, "ops", "laib_net", map[string]any{"view": "geraete"})
	var got netDevices
	if code != cli.ExitOK || json.Unmarshal([]byte(out), &got) != nil {
		t.Fatalf("exit %d: %s", code, out)
	}
	if got.Subnet != "192.0.2.0/24" || len(got.Devices) != 3 || woke != "" {
		t.Fatalf("got %+v, woke %q", got, woke)
	}
	if d := got.Devices[1]; d.Name != "epson-buero.fritz.box" || d.Maker != "Beispiel Drucker GmbH" {
		t.Fatalf("printer %+v", d)
	}
	if !strings.Contains(got.Devices[2].Maker, "zufällig") || !strings.Contains(got.Devices[0].Maker, "zufällig") {
		t.Fatalf("a locally administered MAC is said as such: %+v", got.Devices)
	}
	if !strings.Contains(strings.Join(got.Limits, " "), "ein schlafender Drucker fehlt") {
		t.Fatalf("limits %v", got.Limits)
	}

	out, code = mcp.Call(context.Background(), env, st, "ops", "laib_net", map[string]any{"view": "geraete", "wecken": true})
	if code != cli.ExitOK || woke != "192.0.2.0/24 192.0.2.10" || !strings.Contains(out, `"woken": true`) {
		t.Fatalf("wecken: exit %d, woke %q: %s", code, woke, out)
	}

	ouiFile = filepath.Join(dir, "missing")
	env.JSON = false
	var buf strings.Builder
	env.Out = &buf
	if code := cmdNet(context.Background(), env, []string{"geraete"}); code != cli.ExitOK || !strings.Contains(buf.String(), "192.0.2.33") || !strings.Contains(buf.String(), "kein Herstellerverzeichnis") {
		t.Fatalf("text: exit %d: %s", code, buf.String())
	}
	if !policy.KnownVerbs["net"] {
		t.Fatal("net is not in policy.KnownVerbs")
	}
}
