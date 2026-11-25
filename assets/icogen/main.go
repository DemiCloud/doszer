// Command icogen packs PNG images into a Windows .ico file, each stored as
// PNG (supported since Windows Vista), so a 256-pixel image costs kilobytes
// rather than the 256 KB of an uncompressed bitmap. Used by `make icon`.
//
// Usage: go run ./assets/icogen OUT.ico IN.png...
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image/png"
	"os"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: icogen OUT.ico IN.png...")
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "icogen:", err)
		os.Exit(1)
	}
}

func run(out string, ins []string) error {
	type entry struct {
		w, h int
		data []byte
	}
	var entries []entry
	for _, in := range ins {
		data, err := os.ReadFile(in)
		if err != nil {
			return err
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("%s: %w", in, err)
		}
		if cfg.Width > 256 || cfg.Height > 256 {
			return fmt.Errorf("%s: %dx%d, at most 256x256", in, cfg.Width, cfg.Height)
		}
		entries = append(entries, entry{cfg.Width, cfg.Height, data})
	}

	// ICONDIR, then an ICONDIRENTRY per image, then the images. A width or
	// height of 256 is stored as 0.
	var buf bytes.Buffer
	le := binary.LittleEndian
	binary.Write(&buf, le, [3]uint16{0, 1, uint16(len(entries))})
	offset := 6 + 16*len(entries)
	for _, e := range entries {
		buf.WriteByte(byte(e.w))
		buf.WriteByte(byte(e.h))
		buf.Write([]byte{0, 0})                  // colour count, reserved
		binary.Write(&buf, le, [2]uint16{1, 32}) // planes, bits per pixel
		binary.Write(&buf, le, [2]uint32{uint32(len(e.data)), uint32(offset)})
		offset += len(e.data)
	}
	for _, e := range entries {
		buf.Write(e.data)
	}
	return os.WriteFile(out, buf.Bytes(), 0o644)
}
