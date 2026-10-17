package stick

import (
	"bytes"
	"compress/gzip"
	"fmt"
)

// Initrd builds the embed-area payload: a newc cpio archive holding
// config.ign (mode 0100600), gzip-compressed. coreos-installer writes xz
// with CRC32; the kernel and coreos-installer's reader accept gzip too, and
// gzip is in Go's standard library.
func Initrd(ignition []byte) ([]byte, error) {
	var cpio bytes.Buffer
	writeNewc(&cpio, "config.ign", 0o100600, ignition)
	writeNewc(&cpio, "TRAILER!!!", 0, nil)
	var out bytes.Buffer
	gz, err := gzip.NewWriterLevel(&out, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := gz.Write(cpio.Bytes()); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// writeNewc appends one SVR4 "newc" cpio entry.
func writeNewc(b *bytes.Buffer, name string, mode uint32, data []byte) {
	ino := uint32(1)
	if name == "TRAILER!!!" {
		ino = 0
	}
	fields := []uint32{ino, mode, 0, 0, 1, 0, uint32(len(data)), 0, 0, 0, 0, uint32(len(name) + 1), 0}
	b.WriteString("070701")
	for _, f := range fields {
		fmt.Fprintf(b, "%08x", f)
	}
	b.WriteString(name)
	b.WriteByte(0)
	pad4(b)
	b.Write(data)
	pad4(b)
}

func pad4(b *bytes.Buffer) {
	for b.Len()%4 != 0 {
		b.WriteByte(0)
	}
}
