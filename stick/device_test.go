package stick

import (
	"fmt"
	"strings"
	"testing"
)

// laib-stick wrote to whatever device it was given; --yes skipped the only
// question (#51). Refuse decides before anything is written.
func TestRefuse(t *testing.T) {
	const gb = int64(1) << 30
	cases := []struct {
		name    string
		d       DeviceInfo
		force   bool
		wantErr string // "" = may be written
	}{
		{"a 32 GB USB stick", DeviceInfo{Path: "/dev/sdb", Model: "SanDisk Ultra", Size: 32 * gb, Bus: "usb", Removable: true}, false, ""},
		{"an SD card", DeviceInfo{Path: "/dev/mmcblk0", Model: "SD32G", Size: 32 * gb, Bus: "sd", Removable: true}, false, ""},
		{"the running system's disk", DeviceInfo{Path: "/dev/nvme0n1", Model: "Samsung 980", Size: 512 * gb, Bus: "nvme", System: true}, false, "running system"},
		{"the running system's disk, forced", DeviceInfo{Path: "/dev/nvme0n1", Size: 512 * gb, Bus: "nvme", System: true}, true, "running system"},
		{"an internal SATA disk", DeviceInfo{Path: "/dev/sdc", Model: "WD Blue", Size: 1000 * gb, Bus: "sata"}, false, "not a USB"},
		{"an internal SATA disk, forced", DeviceInfo{Path: "/dev/sdc", Model: "WD Blue", Size: 1000 * gb, Bus: "sata"}, true, ""},
		{"a 2 TB USB backup drive", DeviceInfo{Path: "/dev/sdd", Model: "WD Elements", Size: 2000 * gb, Bus: "usb"}, false, "larger than 256 GB"},
		{"a 2 TB USB backup drive, forced", DeviceInfo{Path: "/dev/sdd", Model: "WD Elements", Size: 2000 * gb, Bus: "usb"}, true, ""},
		{"an unknown size", DeviceInfo{Path: "/dev/sde", Bus: "usb"}, false, "size"},
	}
	for _, c := range cases {
		err := c.d.Refuse(c.force)
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("%s: refused: %v", c.name, err)
		case c.wantErr != "" && err == nil:
			t.Errorf("%s: allowed, want a refusal mentioning %q", c.name, c.wantErr)
		case c.wantErr != "" && !strings.Contains(err.Error(), c.wantErr):
			t.Errorf("%s: refusal %q does not mention %q", c.name, err, c.wantErr)
		}
	}
}

func TestParseLsblkDevice(t *testing.T) {
	d := parseLsblkDevice(`TYPE="disk" SIZE="31914983424" RM="1" TRAN="usb" MODEL="Ultra USB 3.0"`)
	if d.Size != 31914983424 || d.Bus != "usb" || !d.Removable || d.Model != "Ultra USB 3.0" {
		t.Fatalf("parsed %+v", d)
	}
}

// The shape Get-Disk | ConvertTo-Json gives on Windows (taken from a real
// machine: one NVMe system disk), and an array for several disks.
func TestParseWindowsDisk(t *testing.T) {
	d, err := parseWindowsDisk(`{"FriendlyName":"MZ9L8256HDJD-00BMV-SAMSUNG","Size":256060514304,"Bus":"NVMe","IsBoot":true,"IsSystem":true}`)
	if err != nil || d.Bus != "nvme" || !d.System || d.Size != 256060514304 {
		t.Fatalf("parsed %+v, %v", d, err)
	}
	d, err = parseWindowsDisk(`[{"FriendlyName":"SanDisk Cruzer","Size":32015679488,"Bus":"7","IsBoot":false,"IsSystem":false}]`)
	if err != nil || d.Bus != "usb" || d.System || !d.Removable {
		t.Fatalf("numeric bus type 7 is USB: parsed %+v, %v", d, err)
	}
}

// Every MSFT_Disk.BusType number the parser maps, plus names that pass
// through, in both the single-object and the array shape: Removable follows
// the mapped bus, System follows IsBoot or IsSystem.
func TestParseWindowsDiskBusTypes(t *testing.T) {
	cases := []struct {
		bus, wantBus  string
		boot, system  bool
		wantRemovable bool
		wantSystem    bool
	}{
		{"7", "usb", false, false, true, false},
		{"11", "sata", false, true, false, true},
		{"12", "sd", false, false, true, false},
		{"13", "mmc", false, false, true, false},
		{"17", "nvme", true, false, false, true},
		{"USB", "usb", false, false, true, false},
		{"SATA", "sata", false, false, false, false},
		{"99", "99", false, false, false, false},
	}
	for _, c := range cases {
		obj := fmt.Sprintf(`{"FriendlyName":"Disk %s","Size":1024,"Bus":%q,"IsBoot":%t,"IsSystem":%t}`, c.bus, c.bus, c.boot, c.system)
		for _, in := range []string{obj, "  [" + obj + "]\n"} {
			d, err := parseWindowsDisk(in)
			if err != nil {
				t.Fatalf("%s: %v", in, err)
			}
			want := DeviceInfo{Model: "Disk " + c.bus, Size: 1024, Bus: c.wantBus, Removable: c.wantRemovable, System: c.wantSystem}
			if d != want {
				t.Errorf("%s: parsed %+v, want %+v", in, d, want)
			}
		}
	}
	for _, in := range []string{"[]", "", "not json", "[{]"} {
		if d, err := parseWindowsDisk(in); err == nil {
			t.Errorf("%q: parsed %+v, want an error", in, d)
		}
	}
	if _, err := parseWindowsDisk("[]"); err == nil || err.Error() != "Get-Disk returned nothing" {
		t.Errorf("empty array: error %v", err)
	}
}

func TestParseDiskutil(t *testing.T) {
	d := parseDiskutil(`<plist><dict>
	<key>BusProtocol</key><string>USB</string>
	<key>Internal</key><false/>
	<key>MediaName</key><string>SanDisk Ultra</string>
	<key>ParentWholeDisk</key><string>disk4</string>
	<key>RemovableMedia</key><true/>
	<key>TotalSize</key><integer>32015679488</integer>
	</dict></plist>`)
	if d.Bus != "usb" || !d.Removable || d.Size != 32015679488 || d.Model != "SanDisk Ultra" || d.Internal {
		t.Fatalf("parsed %+v", d)
	}
}

// The command the wizard prints for later must not name a real disk: copied
// and run, "--device 2" overwrote a second internal disk on many PCs (#51).
func TestDeviceExampleIsAPlaceholder(t *testing.T) {
	for _, goos := range []string{"windows", "darwin", "linux"} {
		ex := DeviceExample(goos)
		if _, err := diskNumber(ex); err == nil {
			t.Errorf("%s: example %q is a usable disk number", goos, ex)
		}
		if !strings.ContainsAny(ex, "<>NX") {
			t.Errorf("%s: example %q does not look like a placeholder", goos, ex)
		}
	}
}
