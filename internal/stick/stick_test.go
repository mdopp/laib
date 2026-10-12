package stick

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vincent-petithory/dataurl"
)

func box() Box {
	return Box{Hostname: "nuc", Domain: "trial.example", LanIP: "192.168.178.30",
		SSHKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKx test@laptop", ModelURL: "http://192.168.178.100:11435/v1"}
}

func TestDestIgnitionRendersTheBox(t *testing.T) {
	ign, err := DestIgnition(box())
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(ign, &doc); err != nil {
		t.Fatal(err)
	}
	s := string(ign)
	for _, want := range []string{"ssh-ed25519 AAAAC3", "laib-firstboot.service", "/etc/laib/firstboot.env", `"version":"3.4.0"`} {
		if !strings.Contains(s, want) {
			t.Errorf("ignition lacks %q", want)
		}
	}
	if strings.Contains(s, "${") {
		t.Error("unsubstituted placeholder left in the ignition")
	}
	if strings.Contains(s, "passwordHash") {
		t.Error("no password given, but a hash field was rendered")
	}
}

func TestDestIgnitionWithPassword(t *testing.T) {
	b := box()
	b.PassHash = SHA512Crypt("geheim")
	ign, err := DestIgnition(b)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ign), `"passwordHash":"$6$`) {
		t.Error("password hash not rendered")
	}
}

// The VM job must boot the commit under test, not the last release (#38):
// --source puts a base URL into firstboot.env, and first boot fetches
// install-box.sh and the binaries from there.
func TestSourceReachesFirstBoot(t *testing.T) {
	b := box()
	b.Source = "http://10.0.2.2:8000"
	ign, err := DestIgnition(b)
	if err != nil {
		t.Fatal(err)
	}
	files := decodeDataURLs(t, ign)
	for _, want := range []string{"LAIB_SOURCE=http://10.0.2.2:8000\n", `"$LAIB_SOURCE/install-box.sh"`, `--source "$LAIB_SOURCE"`} {
		if !strings.Contains(files, want) {
			t.Errorf("first boot lacks %q", want)
		}
	}
}

// Without --source a box installs the latest release, as before.
func TestNoSourceInstallsTheRelease(t *testing.T) {
	ign, err := DestIgnition(box())
	if err != nil {
		t.Fatal(err)
	}
	files := decodeDataURLs(t, ign)
	if !strings.Contains(files, "LAIB_SOURCE=\n") {
		t.Error("firstboot.env should carry an empty LAIB_SOURCE")
	}
	if !strings.Contains(files, "api.github.com/repos/mdopp/laib/contents/scripts/install-box.sh") {
		t.Error("without a source, first boot must fetch install-box.sh from the repo")
	}
}

func TestSHA512CryptKnownVector(t *testing.T) {
	// from the reference implementation's test suite
	got := sha512CryptSalt("Hello world!", "saltstring")
	want := "$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1"
	if got != want {
		t.Fatalf("sha512crypt:\n got %s\nwant %s", got, want)
	}
}

// A stick left in a USB-first machine boots the live system at every reboot;
// it must not reinstall a box that is already installed (#33). Only a stick
// built with --reinstall may, and the pre-install script knows which it is.
func TestLiveIgnitionGuardsAnInstalledBox(t *testing.T) {
	dest, _ := DestIgnition(box())
	for _, tc := range []struct {
		reinstall bool
		want      string
	}{{false, "export LAIB_REINSTALL=''"}, {true, "export LAIB_REINSTALL='1'"}} {
		live, err := LiveIgnition(dest, "nuc", tc.reinstall)
		if err != nil {
			t.Fatal(err)
		}
		files := decodeDataURLs(t, live)
		if !strings.Contains(files, tc.want) {
			t.Errorf("reinstall=%v: pre-install lacks %q", tc.reinstall, tc.want)
		}
		if !strings.Contains(files, "Diese Box ist schon installiert") {
			t.Error("pre-install has no guard against reinstalling an installed box")
		}
	}
}

func TestLiveIgnitionCarriesDestAndScripts(t *testing.T) {
	dest, _ := DestIgnition(box())
	live, err := LiveIgnition(dest, "nuc", false)
	if err != nil {
		t.Fatal(err)
	}
	s := string(live)
	for _, want := range []string{"/etc/coreos/dest.ign", "/etc/coreos/installer.d/0000-customize.yaml", "pre-install-laib.service", "post-install-laib.service", "RequiredBy=coreos-installer.service", "RequiredBy=coreos-installer.target"} {
		if !strings.Contains(s, want) {
			t.Errorf("live ignition lacks %q", want)
		}
	}
	// file contents travel as data: URLs; decode and check them
	dec := decodeDataURLs(t, live)
	if strings.Contains(dec, "dest-device: /dev/disk") {
		t.Error("customize.yaml must not carry a dest-device: the hook's file is the only one allowed")
	}
	for _, want := range []string{"ignition-file: /etc/coreos/dest.ign", "export SERVER_NAME='nuc'", "0050-dest-device.yaml", "efibootmgr"} {
		if !strings.Contains(dec, want) {
			t.Errorf("live files lack %q", want)
		}
	}
	// the dest ignition travels as a data: URL and must round-trip
	var doc struct {
		Storage struct {
			Files []struct {
				Path     string `json:"path"`
				Contents struct {
					Source string `json:"source"`
				} `json:"contents"`
			} `json:"files"`
		} `json:"storage"`
	}
	json.Unmarshal(live, &doc)
	for _, f := range doc.Storage.Files {
		if f.Path == "/etc/coreos/dest.ign" && !strings.HasPrefix(f.Contents.Source, "data:") {
			t.Errorf("dest.ign is not inlined: %s", f.Contents.Source[:30])
		}
	}
}

// decodeDataURLs concatenates the decoded contents of every storage file.
func decodeDataURLs(t *testing.T, ign []byte) string {
	t.Helper()
	var doc struct {
		Storage struct {
			Files []struct {
				Contents struct {
					Source string `json:"source"`
				} `json:"contents"`
			} `json:"files"`
		} `json:"storage"`
	}
	json.Unmarshal(ign, &doc)
	var out strings.Builder
	for _, f := range doc.Storage.Files {
		u, err := dataurl.DecodeString(f.Contents.Source)
		if err != nil {
			t.Fatalf("decode %s: %v", f.Contents.Source[:20], err)
		}
		out.Write(u.Data)
	}
	return out.String()
}

func TestInitrdIsGzipNewcCpio(t *testing.T) {
	payload, err := Initrd([]byte(`{"ignition":{"version":"3.4.0"}}`))
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(gz)
	if !bytes.HasPrefix(raw, []byte("070701")) {
		t.Fatal("not a newc cpio")
	}
	if !bytes.Contains(raw, []byte("config.ign\x00")) || !bytes.Contains(raw, []byte("TRAILER!!!")) {
		t.Fatal("cpio lacks config.ign or trailer")
	}
	// mode field (second) is 0100600
	if string(raw[14:22]) != "00008180" {
		t.Fatalf("mode = %s, want 00008180", raw[14:22])
	}
	if len(raw)%4 != 0 {
		t.Fatal("cpio not 4-byte aligned")
	}
}

// synthIso builds a minimal ISO 9660 image: PVD at sector 16, a root
// directory at sector 18 holding IMAGES/ (sector 19) with IGNITION.IMG
// (sector 20, 3 sectors long) and, when withInfo, COREOS/IGNINFO.JSON.
func synthIso(t *testing.T, withInfo bool) string {
	t.Helper()
	img := make([]byte, 30*sector)
	rec := func(name string, extent, length uint32, dir bool) []byte {
		if !dir && !strings.HasSuffix(name, ";1") {
			name += ";1"
		}
		n := 33 + len(name)
		if n%2 == 1 {
			n++
		}
		r := make([]byte, n)
		r[0] = byte(n)
		binary.LittleEndian.PutUint32(r[2:], extent)
		binary.BigEndian.PutUint32(r[6:], extent)
		binary.LittleEndian.PutUint32(r[10:], length)
		binary.BigEndian.PutUint32(r[14:], length)
		if dir {
			r[25] = 2
		}
		r[32] = byte(len(name))
		copy(r[33:], name)
		return r
	}
	pvd := img[16*sector:]
	pvd[0] = 1
	copy(pvd[1:], "CD001")
	copy(pvd[156:], rec("\x00", 18, sector, true)[:34])
	root := img[18*sector:]
	off := 0
	for _, r := range [][]byte{rec("\x00", 18, sector, true), rec("\x01", 18, sector, true), rec("IMAGES", 19, sector, true), rec("COREOS", 21, sector, true)} {
		copy(root[off:], r)
		off += len(r)
	}
	images := img[19*sector:]
	copy(images, rec("IGNITION.IMG", 20, 3*sector, false))
	info := `{"file":"images/ignition.img"}` // as the real ISO spells it: lower case, no offset
	if withInfo {
		copy(img[21*sector:], rec("IGNINFO.JSON", 22, uint32(len(info)), false))
		copy(img[22*sector:], info)
	}
	// a short-form 8.3 name as mkisofs would write it
	p := filepath.Join(t.TempDir(), "test.iso")
	os.WriteFile(p, img, 0o644)
	return p
}

func TestEmbedWritesTheArea(t *testing.T) {
	for _, withInfo := range []bool{false, true} {
		p := synthIso(t, withInfo)
		if err := Embed(p, []byte(`{"ignition":{"version":"3.4.0"}}`)); err != nil {
			t.Fatal(err)
		}
		img, _ := os.ReadFile(p)
		area := img[20*sector : 23*sector]
		if area[0] != 0x1f || area[1] != 0x8b {
			t.Fatalf("withInfo=%v: embed area does not start with gzip magic", withInfo)
		}
		if !bytes.Equal(img[23*sector:], make([]byte, 7*sector)) {
			t.Fatal("wrote past the area")
		}
	}
}

func TestEmbedRefusesOversize(t *testing.T) {
	p := synthIso(t, false)
	big := make([]byte, 64*1024) // random bytes do not compress away
	rand.Read(big)
	if err := Embed(p, big); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected a size error, got %v", err)
	}
}

func TestDiskNumber(t *testing.T) {
	for in, want := range map[string]int{"2": 2, "PhysicalDrive3": 3, `\\.\PhysicalDrive4`: 4, " 0 ": 0} {
		if got, err := diskNumber(in); err != nil || got != want {
			t.Errorf("diskNumber(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"/dev/sdb", "", "-1", "disk"} {
		if _, err := diskNumber(in); err == nil {
			t.Errorf("diskNumber(%q) accepted", in)
		}
	}
}

// First boot is a resumable sequence (#37): its marker is written last, the
// token reaches the installer through the environment (never argv, never
// `ps`), and the env file is shredded in an EXIT trap — except on the
// rpm-ostree reboot path, where the post-reboot run still needs it.
func TestFirstBootResumesAndKeepsTheTokenOffArgv(t *testing.T) {
	ign, err := DestIgnition(box())
	if err != nil {
		t.Fatal(err)
	}
	unit := unitFromIgnition(t, ign, "laib-firstboot.service")
	if strings.Contains(unit, "ConditionPathExists=!/var/opt/laib/laib") {
		t.Fatal("the installed binary is not the marker: it lands before init and enable; a reboot in between never resumes")
	}
	if !strings.Contains(unit, "ConditionPathExists=!/etc/laib/installed") || !strings.Contains(unit, "EnvironmentFile=/etc/laib/firstboot.env") {
		t.Fatalf("marker and environment:\n%s", unit)
	}
	script := fileFromIgnition(t, ign, "/usr/local/bin/laib-firstboot.sh")
	if strings.Contains(script, `--token "$LAIB_GH_TOKEN"`) || strings.Contains(script, ". /etc/laib/firstboot.env") {
		t.Fatal("the token must not be passed on argv or sourced by the script")
	}
	if !strings.Contains(script, "trap") || !strings.Contains(script, "shred") {
		t.Fatal("the env file must be shredded in an EXIT trap")
	}
	if !strings.Contains(script, "touch /etc/laib/installed") || strings.LastIndex(script, "touch /etc/laib/installed") < strings.Index(script, "bash /tmp/install-box.sh") {
		t.Fatal("the marker must be written after the installer ran")
	}
	if !strings.Contains(script, "releases/latest") || !strings.Contains(script, "--version") {
		t.Fatal("first boot must fetch the installer of the release it installs, not main's")
	}
}
