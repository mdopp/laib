// laib-stick builds the USB install stick for a Laib box: a few questions,
// then it downloads Fedora CoreOS, bakes the box's Ignition into the ISO
// and (on Linux/macOS) writes the stick. One static binary for the laptop;
// no podman, no butane, no coreos-installer needed there. Everything it
// answers can also be passed as a flag for scripted runs.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/stick"
	"golang.org/x/term"
)

var Version = "dev"

var in = bufio.NewReader(os.Stdin)

func main() {
	var b stick.Box
	var sshKeyPath, password, out, device, streamName, isoPath string
	var yes, printIgn, noGPU, forceDevice bool
	flag.StringVar(&b.Hostname, "hostname", "", "box name, e.g. nuc")
	flag.StringVar(&b.Domain, "domain", "", "domain the services live under, e.g. trial.example")
	flag.StringVar(&b.LanIP, "lan-ip", "", "static LAN address of the box (empty = DHCP)")
	flag.StringVar(&sshKeyPath, "ssh-key", "", "public key file for the core user")
	flag.StringVar(&password, "password", "", "console password for core (empty = none)")
	flag.BoolVar(&b.GPU, "gpu", false, "the box has a GPU and runs llama itself")
	flag.BoolVar(&b.Reinstall, "reinstall", false, "this stick may reinstall a box that is already installed (the data disk is kept); without it, a stick left in an installed box does nothing")
	flag.BoolVar(&noGPU, "no-gpu", false, "no GPU: a remote model answers (--model-url)")
	flag.StringVar(&b.ModelURL, "model-url", "", "OpenAI-compatible URL of a remote model (box without GPU)")
	flag.StringVar(&b.ModelLease, "model-lease", "", "lease URL of the remote llama (optional)")
	flag.StringVar(&b.GHToken, "token", os.Getenv("LAIB_GH_TOKEN"), "GitHub token, only for a private fork or mirror (mdopp/laib and its images are public)")
	flag.StringVar(&b.DNSProvider, "dns-provider", "", "DNS provider with an API for DNS-01 certificates (desec, cloudflare, hetzner, …); the domain's zone must be there")
	flag.StringVar(&b.DNSToken, "dns-token", os.Getenv("LAIB_DNS_TOKEN"), "the DNS provider's API token (becomes the secret DNS_API_TOKEN)")
	flag.StringVar(&b.TLS, "tls", "", "\"internal\": Caddy's own CA instead of Let's Encrypt — a laptop-only trial, phones cannot trust it")
	flag.StringVar(&b.Source, "source", "", "tests only: base URL serving install-box.sh and one build's binaries, instead of the latest release (the VM job)")
	flag.StringVar(&streamName, "stream", "stable", "Fedora CoreOS stream")
	flag.StringVar(&isoPath, "iso", "", "use this downloaded live ISO instead of fetching one")
	flag.StringVar(&out, "out", "", "where to write the customised ISO (default laib-<hostname>.iso)")
	flag.StringVar(&device, "device", "", "USB device to write, e.g. /dev/sdb or /dev/disk4 (empty = ask)")
	flag.BoolVar(&forceDevice, "force-device", false, "write even when --device is not a USB/SD device or is larger than 256 GB (never the system disk; --yes does not imply it)")
	flag.BoolVar(&yes, "yes", false, "no questions: take flags and defaults")
	flag.BoolVar(&printIgn, "print-ignition", false, "only print the box's Ignition and exit")
	flag.Usage = usage
	flag.Parse()

	fmt.Fprintf(os.Stderr, "laib-stick %s — Installationsstick für eine Laib-Box\n\n", Version)
	ask := !yes
	if !stick.CanWriteDevices() {
		fmt.Fprintln(os.Stderr, "Hinweis: ohne Administrator-Rechte kann laib-stick den Stick nicht beschreiben, nur die ISO bauen.")
		fmt.Fprintln(os.Stderr, "         Rechtsklick → \"Als Administrator ausführen\", wenn es den Stick schreiben soll.")
		fmt.Fprintln(os.Stderr)
	}
	if ask && !term.IsTerminal(int(os.Stdin.Fd())) {
		die("stdin is not a terminal; pass the answers as flags with --yes")
	}

	// 1. the box
	b.Hostname = prompt(ask, "Name der Box", b.Hostname, "nuc", required)
	b.Domain = prompt(ask, "Domain der Dienste", b.Domain, "", required)
	b.LanIP = prompt(ask, "Feste LAN-Adresse (leer = DHCP)", b.LanIP, "", nil)

	// 2. access
	if sshKeyPath == "" {
		sshKeyPath = defaultSSHKey()
	}
	for {
		sshKeyPath = prompt(ask, "SSH Public Key (Datei)", sshKeyPath, "", required)
		key, err := stick.ReadSSHKey(expand(sshKeyPath))
		if err == nil {
			b.SSHKey = key
			break
		}
		fmt.Println("  " + err.Error())
		if !ask {
			os.Exit(1)
		}
	}
	if ask && password == "" && yesno("Konsolen-Passwort für core setzen (für den Notfall am Bildschirm)?", false) {
		password = readPassword("  Passwort")
	}
	if password != "" {
		b.PassHash = stick.SHA512Crypt(password)
	}

	// 3. the model
	if ask && !b.GPU && !noGPU {
		b.GPU = yesno("Hat die Box eine GPU (llama läuft dann dort)?", false)
	}
	if !b.GPU {
		b.ModelURL = prompt(ask, "URL eines entfernten Modells, OpenAI-kompatibel (leer = später)", b.ModelURL, "", nil)
		if b.ModelURL != "" {
			probe(b.ModelURL+"/models", "Modell")
			b.ModelLease = prompt(ask, "Lease-URL der entfernten llama (leer = keine)", b.ModelLease, "", nil)
			if b.ModelLease != "" {
				probe(b.ModelLease, "Lease")
			}
		}
	}
	if b.TLS == "" {
		b.DNSProvider = prompt(ask, "DNS-Anbieter mit API für echte Zertifikate (desec, cloudflare, hetzner, …; die Zone der Domain muss dort liegen; leer = Caddys eigene CA, nur für einen Laptop-Versuch)", b.DNSProvider, "", nil)
		if b.DNSProvider != "" {
			b.DNSToken = prompt(ask, "API-Token des DNS-Anbieters", b.DNSToken, "", nil)
			if b.DNSToken == "" {
				die("ohne Token kein DNS-01: --dns-token oder LAIB_DNS_TOKEN")
			}
		} else {
			b.TLS = "internal"
			fmt.Fprintln(os.Stderr, "Hinweis: ohne DNS-Anbieter signiert Caddy selbst (tls: internal); Handys und ihre Widgets vertrauen dem nicht (#45)")
		}
	}
	if ask && !b.Reinstall {
		b.Reinstall = yesno("Soll dieser Stick eine schon installierte Box neu aufsetzen? (Die Datenplatte bleibt. Ohne Ja tut ein vergessener Stick nichts.)", false)
	}

	dest, err := stick.DestIgnition(b)
	if err != nil {
		die(err.Error())
	}
	if printIgn {
		os.Stdout.Write(dest)
		fmt.Println()
		return
	}
	live, err := stick.LiveIgnition(dest, b.Hostname, b.Reinstall)
	if err != nil {
		die(err.Error())
	}

	// 4. the ISO
	if out == "" {
		out = "laib-" + b.Hostname + ".iso"
	}
	if isoPath == "" {
		fmt.Printf("\nLade Fedora CoreOS (%s) …\n", streamName)
		art, err := stick.Stream(streamName)
		if err != nil {
			die("stream: " + err.Error())
		}
		cache := filepath.Join(cacheDir(), "laib-stick")
		isoPath, err = stick.Download(art, cache, progress)
		if err != nil {
			die("download: " + err.Error())
		}
		fmt.Printf("\n  Release %s, geprüft (sha256), in %s\n", art.Release, isoPath)
	}
	if err := copyFile(isoPath, out); err != nil {
		die(err.Error())
	}
	if err := stick.Embed(out, live); err != nil {
		die("embed: " + err.Error())
	}
	st, _ := os.Stat(out)
	fmt.Printf("\nISO fertig: %s (%d MB) — Ignition eingebettet\n", out, st.Size()>>20)

	// 5. the stick
	if device == "" && ask {
		listDevices()
		device = prompt(true, "USB-Gerät beschreiben (leer = nur die ISO behalten)", "", "", nil)
	}
	if device == "" {
		fmt.Println("\nDie ISO später auf einen Stick schreiben (das Gerät des Sticks einsetzen):")
		fmt.Printf("  laib-stick --yes --iso %s --out %s --device %s\n", out, out, stick.DeviceExample(runtime.GOOS))
		fmt.Println("Dann die Box vom Stick booten; sie installiert sich und startet neu.")
		return
	}
	// what the device is, before anything is written (#51): the system disk
	// never, anything but a USB/SD device up to 256 GB only with --force-device
	info, err := stick.InspectDevice(device)
	if err != nil {
		die(err.Error())
	}
	fmt.Println("\nZiel:", info)
	if err := info.Refuse(forceDevice); err != nil {
		die(err.Error())
	}
	if ask && !yesno(fmt.Sprintf("ALLES auf %s wird gelöscht. Wirklich schreiben?", info), false) {
		fmt.Println("nicht geschrieben; die ISO liegt in", out)
		return
	}
	if err := stick.WriteDevice(out, device, progress); err != nil {
		die(err.Error())
	}
	fmt.Println()
	fmt.Println("\nStick geschrieben. Box vom Stick booten: sie installiert Fedora CoreOS auf die kleinste")
	fmt.Println("interne Platte, startet neu und holt sich Laib. Danach: ssh core@" + b.Hostname + " und `laib status`,")
	fmt.Println("dann `laib recovery-sheet`: die eine Seite, die nach einem Plattenverlust alles zurückholt — ausdrucken.")
}

// probe tells whether a URL answers from this laptop: the box will sit on
// the same LAN, so an unreachable model here is almost surely unreachable
// there. A warning, never a stop (the model may be off right now).
func probe(url, what string) {
	c := &http.Client{Timeout: 4 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		fmt.Printf("  Hinweis: %s unter %s antwortet von hier nicht (%v)\n", what, url, err)
		return
	}
	resp.Body.Close()
	fmt.Printf("  %s antwortet (HTTP %d)\n", what, resp.StatusCode)
}

func usage() {
	fmt.Fprintf(os.Stderr, `laib-stick [flags]

Fragt die Box ab, lädt Fedora CoreOS, bettet die Ignition ein und schreibt
den Stick. Ohne Flags ein Dialog; mit --yes nimmt es nur die Flags.

`)
	flag.PrintDefaults()
}

// prompt asks when interactive, otherwise takes the flag or the default.
func prompt(ask bool, label, current, def string, check func(string) error) string {
	if current == "" {
		current = def
	}
	for {
		v := current
		if ask {
			if current != "" {
				fmt.Printf("%s [%s]: ", label, current)
			} else {
				fmt.Printf("%s: ", label)
			}
			line, _ := in.ReadString('\n')
			line = strings.TrimSpace(line)
			if line != "" {
				v = line
			}
		}
		if check == nil {
			return v
		}
		if err := check(v); err == nil {
			return v
		} else {
			fmt.Println("  " + err.Error())
			if !ask {
				os.Exit(1)
			}
		}
	}
}

func required(v string) error {
	if strings.TrimSpace(v) == "" {
		return fmt.Errorf("bitte einen Wert angeben")
	}
	return nil
}

func yesno(label string, def bool) bool {
	hint := "j/N"
	if def {
		hint = "J/n"
	}
	fmt.Printf("%s [%s]: ", label, hint)
	line, _ := in.ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" {
		return def
	}
	return line == "j" || line == "y" || line == "ja" || line == "yes"
}

func readPassword(label string) string {
	for {
		fmt.Print(label + ": ")
		p1, _ := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Print("\n" + label + " (wiederholen): ")
		p2, _ := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if string(p1) == string(p2) && len(p1) > 0 {
			return string(p1)
		}
		fmt.Println("  stimmt nicht überein oder leer — nochmal")
	}
}

func defaultSSHKey() string {
	home, _ := os.UserHomeDir()
	for _, n := range []string{"id_ed25519.pub", "id_ecdsa.pub", "id_rsa.pub"} {
		p := filepath.Join(home, ".ssh", n)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}

func cacheDir() string {
	if d, err := os.UserCacheDir(); err == nil {
		return d
	}
	return os.TempDir()
}

func progress(done, total int64) {
	if total > 0 {
		fmt.Printf("\r  %3d%%  %d / %d MB", done*100/total, done>>20, total>>20)
	} else {
		fmt.Printf("\r  %d MB", done>>20)
	}
}

func copyFile(src, dst string) error {
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer s.Close()
	d, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(d, s); err != nil {
		d.Close()
		return err
	}
	return d.Close()
}

// listDevices shows the disks the OS knows, best effort.
func listDevices() {
	c := stick.ListDevicesCommand()
	outb, err := exec.Command(c[0], c[1:]...).CombinedOutput()
	if err == nil {
		fmt.Println("\nLaufwerke:")
		fmt.Println(indent(string(outb)))
	}
	if runtime.GOOS == "windows" {
		fmt.Println("  (Gerät = die Nummer aus der Index-Spalte, z.B. 2)")
	}
}

func indent(s string) string {
	return "  " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n  ")
}

func die(msg string) {
	fmt.Fprintln(os.Stderr, "laib-stick:", msg)
	os.Exit(1)
}
