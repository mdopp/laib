package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/state"
)

// The host's view of the LAN (#577). Seams for the tests: the neighbour
// table, the IEEE OUI file the distribution ships (laib ships none), the
// reverse lookup and the knock.
var (
	neighFile  = "/proc/net/arp"
	ouiFile    = "/usr/share/hwdata/oui.txt"
	lookupName = reverseName
	wakeLAN    = knock
)

type netDevice struct {
	IP    string `json:"ip"`
	MAC   string `json:"mac"`
	Name  string `json:"name,omitempty"`
	Maker string `json:"maker,omitempty"`
	Iface string `json:"iface"`
}

type netDevices struct {
	Subnet  string      `json:"subnet"`
	Woken   bool        `json:"woken"`
	Devices []netDevice `json:"devices"`
	Limits  []string    `json:"limits"` // what this list cannot see, for the reader
}

// parseARP keeps the complete entries (ATF_COM) of /proc/net/arp, inside
// the prefix when one is given.
func parseARP(r io.Reader, in netip.Prefix) []netDevice {
	var out []netDevice
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 6 {
			continue
		}
		ip, err := netip.ParseAddr(f[0])
		flags, ferr := strconv.ParseUint(f[2], 0, 32)
		if err != nil || ferr != nil || flags&2 == 0 || len(f[3]) != 17 || f[3] == "00:00:00:00:00:00" || (in.IsValid() && !in.Contains(ip)) {
			continue
		}
		out = append(out, netDevice{IP: f[0], MAC: strings.ToLower(f[3]), Iface: f[5]})
	}
	return out
}

// makers reads an IEEE oui.txt: "00-00-5E   (hex)		<organisation>",
// keyed as a MAC's first three octets.
func makers(r io.Reader) map[string]string {
	m := map[string]string{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		pre, org, ok := strings.Cut(sc.Text(), "(hex)")
		if ok && len(strings.TrimSpace(pre)) == 8 {
			m[strings.ToLower(strings.ReplaceAll(strings.TrimSpace(pre), "-", ":"))] = strings.TrimSpace(org)
		}
	}
	return m
}

func reverseName(ip string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	names, _ := net.DefaultResolver.LookupAddr(ctx, ip)
	if len(names) == 0 {
		return ""
	}
	return strings.TrimSuffix(names[0], ".")
}

// knock sends one UDP datagram to the discard port of every other address
// of the /24: the kernel asks ARP for each, and whoever answers lands in the
// neighbour table. No port is read, nothing listens for a reply.
func knock(p netip.Prefix, self netip.Addr) {
	b := p.Addr().As4()
	for i := 1; i < 255; i++ {
		b[3] = byte(i)
		if a := netip.AddrFrom4(b); a != self {
			if c, err := net.Dial("udp", netip.AddrPortFrom(a, 9).String()); err == nil {
				c.Write([]byte{0})
				c.Close()
			}
		}
	}
	time.Sleep(3 * time.Second)
}

// cmdNetDevices is `laib net geraete [--wecken]` (#577): the devices the
// box's neighbour table holds for its LAN, read only; --wecken knocks once.
func cmdNetDevices(env *cliEnv, st *state.State, args []string) int {
	if len(args) > 1 || (len(args) == 1 && args[0] != "--wecken") {
		return env.Fail(fmt.Errorf("usage: laib net geraete [--wecken]"))
	}
	self, err := netip.ParseAddr(st.Box.LanIP)
	if err != nil || !self.Is4() {
		return cli.Refused(env, "box.yml hat keine IPv4-Adresse (lanIp); ohne sie weiß laib nicht, welches Netz das Heimnetz ist.")
	}
	sub := netip.PrefixFrom(self, 24).Masked()
	res := netDevices{Subnet: sub.String(), Woken: len(args) == 1}
	if res.Woken {
		wakeLAN(sub, self)
	}
	arp, err := os.ReadFile(neighFile)
	if err != nil {
		return env.Fail(fmt.Errorf("Nachbartabelle %s: %w", neighFile, err))
	}
	res.Devices = append([]netDevice{}, parseARP(strings.NewReader(string(arp)), sub)...)
	limit := "nur Geräte, die gerade antworten oder kürzlich mit der Box gesprochen haben; ein schlafender Drucker fehlt"
	if !res.Woken {
		limit += "; laib net geraete --wecken klopft vorher einmal bei jeder Adresse an"
	}
	res.Limits = []string{limit, "nur das Netz " + res.Subnet + " (IPv4); Geräte im Gast-WLAN oder hinter einem anderen Router fehlen",
		"Namen aus dem Reverse-DNS (meist der Router); ohne Eintrag dort bleibt der Name leer"}
	var oui map[string]string
	if b, err := os.ReadFile(ouiFile); err == nil {
		oui = makers(strings.NewReader(string(b)))
	} else {
		res.Limits = append(res.Limits, "kein Herstellerverzeichnis auf der Box ("+ouiFile+"); der Hersteller bleibt leer")
	}
	var wg sync.WaitGroup
	for i := range res.Devices {
		d := &res.Devices[i]
		if b, err := strconv.ParseUint(d.MAC[:2], 16, 8); err == nil && b&2 != 0 {
			d.Maker = "zufällige Adresse (Handy oder Laptop mit Datenschutz)"
		} else {
			d.Maker = oui[d.MAC[:8]]
		}
		wg.Add(1)
		go func() { defer wg.Done(); d.Name = lookupName(d.IP) }()
	}
	wg.Wait()
	if env.JSON {
		return cli.Emit(env, res)
	}
	w := tabwriter.NewWriter(env.W(), 0, 2, 2, ' ', 0)
	fmt.Fprintf(w, "Adresse\tMAC\tName\tHersteller\n")
	for _, d := range res.Devices {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", d.IP, d.MAC, d.Name, d.Maker)
	}
	w.Flush()
	fmt.Fprintf(env.W(), "\nGrenzen:\n- %s\n", strings.Join(res.Limits, "\n- "))
	return cli.ExitOK
}
