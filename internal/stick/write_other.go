//go:build !windows

package stick

import (
	"fmt"
	"os"
	"os/exec"
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

// ListDevicesCommand is the shell command that lists removable disks.
func ListDevicesCommand() []string {
	if runtime.GOOS == "darwin" {
		return []string{"diskutil", "list", "external", "physical"}
	}
	return []string{"lsblk", "-d", "-o", "NAME,SIZE,MODEL,TRAN,RM"}
}
