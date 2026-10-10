//go:build windows

package stick

import (
	"fmt"
	"io"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Raw-disk writing on Windows, what Rufus' "DD mode" does: take the
// physical drive, lock and dismount every volume that lives on it (so
// Explorer and the file-system driver let go), then write the image with
// unbuffered, sector-aligned I/O to \\.\PhysicalDriveN. Needs an elevated
// (Administrator) process.
const (
	fsctlLockVolume                 = 0x00090018
	fsctlDismountVolume             = 0x00090020
	ioctlVolumeGetVolumeDiskExtents = 0x00560000
	chunk                           = 4 << 20
	sectorAlign                     = 4096
)

type diskExtent struct {
	DiskNumber     uint32
	_              uint32
	StartingOffset int64
	ExtentLength   int64
}

type volumeDiskExtents struct {
	NumberOfDiskExtents uint32
	_                   uint32
	Extents             [8]diskExtent
}

// CanWriteDevices reports whether this process may open physical drives.
func CanWriteDevices() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// volumesOnDisk finds the drive letters whose volume sits on disk n.
func volumesOnDisk(n int) []string {
	var found []string
	for l := 'C'; l <= 'Z'; l++ {
		path, _ := windows.UTF16PtrFromString(fmt.Sprintf(`\\.\%c:`, l))
		h, err := windows.CreateFile(path, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
		if err != nil {
			continue
		}
		var ext volumeDiskExtents
		var got uint32
		err = windows.DeviceIoControl(h, ioctlVolumeGetVolumeDiskExtents, nil, 0, (*byte)(unsafe.Pointer(&ext)), uint32(unsafe.Sizeof(ext)), &got, nil)
		windows.CloseHandle(h)
		if err != nil {
			continue
		}
		for i := 0; i < int(ext.NumberOfDiskExtents) && i < len(ext.Extents); i++ {
			if int(ext.Extents[i].DiskNumber) == n {
				found = append(found, fmt.Sprintf("%c:", l))
				break
			}
		}
	}
	return found
}

// WriteDevice writes the image file to physical disk `device`.
func WriteDevice(image, device string, progress func(done, total int64)) error {
	n, err := diskNumber(device)
	if err != nil {
		return err
	}
	if !CanWriteDevices() {
		return fmt.Errorf("writing a disk needs Administrator rights: start laib-stick as Administrator")
	}
	src, err := os.Open(image)
	if err != nil {
		return err
	}
	defer src.Close()
	st, _ := src.Stat()
	total := st.Size()

	// lock + dismount every volume on the disk; the handles stay open
	// (and thus locked) until the write is through
	var locks []windows.Handle
	defer func() {
		for _, h := range locks {
			windows.CloseHandle(h)
		}
	}()
	for _, v := range volumesOnDisk(n) {
		path, _ := windows.UTF16PtrFromString(`\\.\` + v)
		h, err := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
		if err != nil {
			return fmt.Errorf("open volume %s: %w", v, err)
		}
		var got uint32
		if err := windows.DeviceIoControl(h, fsctlLockVolume, nil, 0, nil, 0, &got, nil); err != nil {
			windows.CloseHandle(h)
			return fmt.Errorf("lock %s (close every window and program using the stick): %w", v, err)
		}
		if err := windows.DeviceIoControl(h, fsctlDismountVolume, nil, 0, nil, 0, &got, nil); err != nil {
			windows.CloseHandle(h)
			return fmt.Errorf("dismount %s: %w", v, err)
		}
		locks = append(locks, h)
	}

	path, _ := windows.UTF16PtrFromString(fmt.Sprintf(`\\.\PhysicalDrive%d`, n))
	disk, err := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_NO_BUFFERING|windows.FILE_FLAG_WRITE_THROUGH, 0)
	if err != nil {
		return fmt.Errorf("open PhysicalDrive%d: %w", n, err)
	}
	defer windows.CloseHandle(disk)

	// unbuffered I/O wants sector-aligned buffers and lengths
	raw := make([]byte, chunk+sectorAlign)
	off := sectorAlign - int(uintptr(unsafe.Pointer(&raw[0]))%sectorAlign)
	buf := raw[off : off+chunk]
	var done int64
	for {
		nr, rerr := io.ReadFull(src, buf)
		if nr == 0 && rerr != nil {
			break
		}
		wlen := (nr + 511) / 512 * 512
		for i := nr; i < wlen; i++ {
			buf[i] = 0
		}
		var written uint32
		if err := windows.WriteFile(disk, buf[:wlen], &written, nil); err != nil {
			return fmt.Errorf("write at %d MB: %w", done>>20, err)
		}
		done += int64(nr)
		if progress != nil {
			progress(done, total)
		}
		if rerr != nil {
			break
		}
	}
	if err := windows.FlushFileBuffers(disk); err != nil {
		return err
	}
	return nil
}

// ListDevicesCommand is the shell command that lists physical disks.
func ListDevicesCommand() []string {
	return []string{"powershell", "-NoProfile", "-Command",
		"Get-CimInstance Win32_DiskDrive | Sort-Object Index | Format-Table -AutoSize Index,Model,InterfaceType,@{n='GB';e={[math]::Round($_.Size/1GB)}}"}
}
