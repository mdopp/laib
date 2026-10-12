package stick

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// diskNumber turns "2", "PhysicalDrive2" or "\\.\PhysicalDrive2" (Windows
// spellings) into the disk number.
func diskNumber(device string) (int, error) {
	s := strings.ToLower(strings.TrimSpace(device))
	s = strings.TrimPrefix(s, `\\.\`)
	s = strings.TrimPrefix(s, "physicaldrive")
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%q is not a disk number (e.g. 2 or \\\\.\\PhysicalDrive2)", device)
	}
	return n, nil
}

// DeviceInfo is what laib-stick knows about a target device before it writes
// the image (#51).
type DeviceInfo struct {
	Path      string
	Model     string
	Size      int64
	Bus       string // usb, sd, mmc, sata, nvme, …
	Removable bool
	Internal  bool // macOS says so
	System    bool // holds the running system: never written
}

// maxStick is the largest device laib-stick writes without --force-device: a
// "stick" larger than this is most likely a backup drive or a disk.
const maxStick = int64(256) << 30

func (d DeviceInfo) String() string {
	model := d.Model
	if model == "" {
		model = "unbekanntes Modell"
	}
	return fmt.Sprintf("%s (%s, %.1f GB, %s)", d.Path, model, float64(d.Size)/1e9, strings.ToUpper(d.Bus))
}

// Refuse says why the device must not be written, or nil. The running
// system's disk is refused always; a device that is not USB/SD or is larger
// than 256 GB only with force (--force-device, never implied by --yes).
func (d DeviceInfo) Refuse(force bool) error {
	if d.System {
		return fmt.Errorf("%s holds the running system — nothing was written", d)
	}
	if force {
		return nil
	}
	var why []string
	stickish := d.Bus == "usb" || d.Bus == "sd" || d.Bus == "mmc" || (d.Removable && !d.Internal)
	if !stickish {
		why = append(why, "it is not a USB or SD device")
	}
	switch {
	case d.Size <= 0:
		why = append(why, "its size is unknown")
	case d.Size > maxStick:
		why = append(why, "it is larger than 256 GB")
	}
	if len(why) == 0 {
		return nil
	}
	return fmt.Errorf("%s does not look like an install stick: %s. Nothing was written. If it really is the stick: --force-device", d, strings.Join(why, "; "))
}

// DeviceExample is the device the wizard prints in the command for later: a
// placeholder that cannot name a real disk when it is copied as it is.
func DeviceExample(goos string) string {
	switch goos {
	case "windows":
		return "<NUMMER>"
	case "darwin":
		return "/dev/diskN"
	}
	return "/dev/sdX"
}

// run executes a command for InspectDevice; swapped in tests.
var run = func(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

var lsblkPair = regexp.MustCompile(`(\w+)="([^"]*)"`)

// parseLsblkDevice reads `lsblk -dnbP -o TYPE,SIZE,RM,TRAN,MODEL <dev>`.
func parseLsblkDevice(out string) DeviceInfo {
	kv := map[string]string{}
	for _, m := range lsblkPair.FindAllStringSubmatch(out, -1) {
		kv[m[1]] = m[2]
	}
	size, _ := strconv.ParseInt(kv["SIZE"], 10, 64)
	return DeviceInfo{Model: strings.TrimSpace(kv["MODEL"]), Size: size, Bus: strings.ToLower(kv["TRAN"]), Removable: kv["RM"] == "1"}
}

// parseWindowsDisk reads `Get-Disk -Number N | Select-Object FriendlyName,
// Size, @{n='Bus';e={"$($_.BusType)"}}, IsBoot, IsSystem | ConvertTo-Json`:
// one object, or an array of one. BusType may come as its name or its number.
func parseWindowsDisk(out string) (DeviceInfo, error) {
	type disk struct {
		FriendlyName     string
		Size             int64
		Bus              string
		IsBoot, IsSystem bool
	}
	out = strings.TrimSpace(out)
	var list []disk
	if strings.HasPrefix(out, "[") {
		if err := json.Unmarshal([]byte(out), &list); err != nil {
			return DeviceInfo{}, err
		}
	} else {
		var one disk
		if err := json.Unmarshal([]byte(out), &one); err != nil {
			return DeviceInfo{}, err
		}
		list = append(list, one)
	}
	if len(list) == 0 {
		return DeviceInfo{}, fmt.Errorf("Get-Disk returned nothing")
	}
	d := list[0]
	bus := strings.ToLower(d.Bus)
	switch bus { // MSFT_Disk.BusType numbers
	case "7":
		bus = "usb"
	case "12":
		bus = "sd"
	case "13":
		bus = "mmc"
	case "11":
		bus = "sata"
	case "17":
		bus = "nvme"
	}
	return DeviceInfo{Model: d.FriendlyName, Size: d.Size, Bus: bus, Removable: bus == "usb" || bus == "sd" || bus == "mmc", System: d.IsBoot || d.IsSystem}, nil
}

var plistEntry = regexp.MustCompile(`<key>(\w+)</key>\s*<(?:(string|integer)>([^<]*)</(?:string|integer)|(true|false)/)>`)

// parseDiskutil reads `diskutil info -plist <disk>` (macOS).
func parseDiskutil(out string) DeviceInfo {
	kv := map[string]string{}
	for _, m := range plistEntry.FindAllStringSubmatch(out, -1) {
		if m[4] != "" {
			kv[m[1]] = m[4]
		} else {
			kv[m[1]] = m[3]
		}
	}
	size, _ := strconv.ParseInt(kv["TotalSize"], 10, 64)
	if size == 0 {
		size, _ = strconv.ParseInt(kv["Size"], 10, 64)
	}
	model := kv["MediaName"]
	if model == "" {
		model = kv["IORegistryEntryName"]
	}
	return DeviceInfo{Model: model, Size: size, Bus: strings.ToLower(kv["BusProtocol"]),
		Removable: kv["RemovableMedia"] == "true" || kv["Removable"] == "true", Internal: kv["Internal"] == "true"}
}
