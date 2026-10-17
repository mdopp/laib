package stick

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const sector = 2048

// isoFile is a file's extent in the ISO 9660 image.
type isoFile struct {
	Offset int64
	Length int64
}

// lookup walks the ISO 9660 directory tree for an upper-case path like
// IMAGES/IGNITION.IMG (identifiers compared without the ";1" version).
func lookup(r io.ReaderAt, path string) (isoFile, error) {
	pvd := make([]byte, sector)
	if _, err := r.ReadAt(pvd, 16*sector); err != nil {
		return isoFile{}, err
	}
	if string(pvd[1:6]) != "CD001" {
		return isoFile{}, errors.New("not an ISO 9660 image")
	}
	// the root directory record sits at byte 156 of the PVD
	dir := isoFile{Offset: int64(binary.LittleEndian.Uint32(pvd[156+2:])) * sector, Length: int64(binary.LittleEndian.Uint32(pvd[156+10:]))}
	parts := strings.Split(strings.ToUpper(path), "/")
	for i, want := range parts {
		buf := make([]byte, dir.Length)
		if _, err := r.ReadAt(buf, dir.Offset); err != nil {
			return isoFile{}, err
		}
		found := false
		for pos := int64(0); pos < dir.Length; {
			rec := buf[pos:]
			n := int64(rec[0])
			if n == 0 { // end of this sector's records; continue at the next sector
				pos = (pos/sector + 1) * sector
				continue
			}
			nameLen := int(rec[32])
			name := string(rec[33 : 33+nameLen])
			if j := strings.IndexByte(name, ';'); j >= 0 {
				name = name[:j]
			}
			if name == want {
				dir = isoFile{Offset: int64(binary.LittleEndian.Uint32(rec[2:])) * sector, Length: int64(binary.LittleEndian.Uint32(rec[10:]))}
				found = true
				break
			}
			pos += n
		}
		if !found {
			return isoFile{}, fmt.Errorf("%s not found in the ISO (at %q)", path, parts[i])
		}
	}
	return dir, nil
}

// EmbedArea finds where the Ignition initrd goes: COREOS/IGNINFO.JSON when
// present (file, offset, length), else IMAGES/IGNITION.IMG as a whole.
func EmbedArea(r io.ReaderAt) (isoFile, error) {
	if info, err := lookup(r, "COREOS/IGNINFO.JSO"); err == nil {
		raw := make([]byte, info.Length)
		if _, err := r.ReadAt(raw, info.Offset); err != nil {
			return isoFile{}, err
		}
		var ii struct {
			File   string `json:"file"`
			Offset int64  `json:"offset"`
			Length int64  `json:"length"`
		}
		if err := json.Unmarshal(bytes.TrimRight(raw, "\x00"), &ii); err == nil && ii.File != "" {
			f, err := lookup(r, ii.File)
			if err != nil {
				return isoFile{}, err
			}
			if ii.Length == 0 {
				ii.Length = f.Length - ii.Offset
			}
			return isoFile{Offset: f.Offset + ii.Offset, Length: ii.Length}, nil
		}
	}
	return lookup(r, "IMAGES/IGNITION.IMG")
}

// Embed writes the Ignition initrd into the ISO file at path, zero-padded
// to the area's length, refusing when it does not fit.
func Embed(isoPath string, ignition []byte) error {
	f, err := os.OpenFile(isoPath, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	area, err := EmbedArea(f)
	if err != nil {
		return err
	}
	payload, err := Initrd(ignition)
	if err != nil {
		return err
	}
	if int64(len(payload)) > area.Length {
		return fmt.Errorf("the Ignition is too large for the ISO's embed area: %d > %d bytes", len(payload), area.Length)
	}
	buf := make([]byte, area.Length)
	copy(buf, payload)
	if _, err := f.WriteAt(buf, area.Offset); err != nil {
		return err
	}
	return f.Sync()
}
