//go:build !windows

package stick

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// CanWriteDevices is true on Linux/macOS: dd runs under sudo and asks.
func CanWriteDevices() bool { return true }

// WriteDevice writes the image with dd under sudo (transparent, and the
// one tool every Linux/macOS has). macOS first unmounts the disk and
// writes the raw device.
func WriteDevice(image, device string, progress func(done, total int64)) error {
	var cmds [][]string
	switch runtime.GOOS {
	case "darwin":
		raw := strings.Replace(device, "/dev/disk", "/dev/rdisk", 1)
		cmds = [][]string{{"diskutil", "unmountDisk", device}, {"sudo", "dd", "if=" + image, "of=" + raw, "bs=4m"}, {"diskutil", "eject", device}}
	default:
		cmds = [][]string{{"sudo", "dd", "if=" + image, "of=" + device, "bs=4M", "status=progress", "conv=fsync"}}
	}
	for _, c := range cmds {
		fmt.Println("$", strings.Join(c, " "))
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s: %w", c[0], err)
		}
	}
	return nil
}

var diskName = regexp.MustCompile(`disk\d+`)

// InspectDevice reads what the device is, without changing it (#51).
func InspectDevice(device string) (DeviceInfo, error) {
	if runtime.GOOS == "darwin" {
		out, err := run("diskutil", "info", "-plist", device)
		if err != nil {
			return DeviceInfo{Path: device}, fmt.Errorf("diskutil info %s: %v: %s", device, err, out)
		}
		d := parseDiskutil(out)
		d.Path = device
		// the disks behind / : its volume, container and APFS physical stores
		whole := strings.TrimPrefix(strings.TrimPrefix(device, "/dev/"), "r")
		if root, err := run("diskutil", "info", "-plist", "/"); err == nil {
			for _, n := range diskName.FindAllString(root, -1) {
				if n == whole {
					d.System = true
				}
			}
		}
		return d, nil
	}
	out, err := run("lsblk", "-dnbP", "-o", "TYPE,SIZE,RM,TRAN,MODEL", device)
	if err != nil {
		return DeviceInfo{Path: device}, fmt.Errorf("lsblk %s: %v: %s", device, err, out)
	}
	if !strings.Contains(out, `TYPE="disk"`) {
		return DeviceInfo{Path: device}, fmt.Errorf("%s is not a whole disk", device)
	}
	d := parseLsblkDevice(out)
	d.Path = device
	// the disks under / : lsblk --inverse lists the root device and everything it sits on
	if src, err := run("findmnt", "-n", "-o", "SOURCE", "/"); err == nil {
		if under, err := run("lsblk", "-nrso", "NAME", src); err == nil {
			for _, n := range strings.Fields(under) {
				if n == filepath.Base(device) {
					d.System = true
				}
			}
		}
	}
	return d, nil
}

// ListDevicesCommand is the shell command that lists removable disks.
func ListDevicesCommand() []string {
	if runtime.GOOS == "darwin" {
		return []string{"diskutil", "list", "external", "physical"}
	}
	return []string{"lsblk", "-d", "-o", "NAME,SIZE,MODEL,TRAN,RM"}
}
