// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Command stampedversion prints the version a build stamped into an aggrega
// binary's main.version, read from the binary's own symbol table and data
// rather than by running it — so a binary built for another system is read on
// this one. It reads the three executable formats a release carries: ELF
// (Linux), PE (Windows) and Mach-O (macOS), each through the standard
// library's reader for that format.
//
// The stamp is a Go string variable the linker set with -X. The symbol table
// gives the variable's address; the variable holds a string header, a pointer
// and a length in the binary's byte order and pointer width; and the pointer is
// the address of the version's bytes. Each address is turned into a file offset
// through the section that holds it, counting only sections whose bytes the
// file stores. A variable no -X flag set holds the zero string, and one set to
// an empty value holds no bytes; both are refused as carrying no stamped
// version rather than read as an empty one, and so is a binary with no symbol
// table.
//
// `go version -m` cannot answer this: Go leaves the linker flags out of the
// build information of a -trimpath build.
//
// It is a build tool, not a shipped command: the CLI's build script runs it to
// hold each binary it writes to the version that binary must carry. It writes
// exactly one line to standard output, the version; on failure it writes the
// reason to standard error and exits 1.
package main

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

// stampVariable is the variable the build stamps, as -X main.version=<version>.
const stampVariable = "main.version"

func main() {
	path := flag.String("binary", "", "path to the built binary to read the stamped version from")
	flag.Parse()

	if *path == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "stampedversion: -binary <path> is required, and nothing else is read")
		os.Exit(2)
	}

	version, err := readFile(*path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stampedversion: %s: %v\n", *path, err)
		os.Exit(1)
	}
	fmt.Println(version)
}

// readFile returns the version stamped into the binary at path.
func readFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return read(f)
}

// image is what reading the stamp needs from an executable, whatever its
// format: the stamp variable's address, the sections whose bytes the file
// stores, and how the format lays out a pointer.
type image struct {
	r        io.ReaderAt
	order    binary.ByteOrder
	ptrSize  uint64
	addr     uint64
	sections []storedSection
}

// storedSection is a section whose bytes are in the file: size bytes loaded at
// addr, stored from offset.
type storedSection struct {
	addr, size, offset uint64
}

// errNoStampSymbol is the refusal for a binary whose symbol table does not name
// the stamp variable.
var errNoStampSymbol = fmt.Errorf("its symbol table has no %s", stampVariable)

// read returns the version stamped into the executable r holds, after naming
// its format from the file's first bytes.
func read(r io.ReaderAt) (string, error) {
	var magic [4]byte
	if _, err := r.ReadAt(magic[:], 0); err != nil {
		return "", fmt.Errorf("read the file's first bytes: %w", err)
	}

	var (
		img image
		err error
	)
	switch {
	case bytes.Equal(magic[:], []byte(elf.ELFMAG)):
		img, err = openELF(r)
	case magic[0] == 'M' && magic[1] == 'Z':
		img, err = openPE(r)
	case isMachO(magic):
		img, err = openMachO(r)
	case binary.BigEndian.Uint32(magic[:]) == macho.MagicFat:
		return "", errors.New("a universal macOS binary holds one program per architecture; read each architecture's own binary")
	default:
		return "", errors.New("not an ELF, PE or Mach-O executable")
	}
	if err != nil {
		return "", err
	}
	return img.stamp()
}

// isMachO reports whether magic opens a single-architecture Mach-O file, in
// either byte order.
func isMachO(magic [4]byte) bool {
	for _, m := range []uint32{binary.LittleEndian.Uint32(magic[:]), binary.BigEndian.Uint32(magic[:])} {
		if m == macho.Magic32 || m == macho.Magic64 {
			return true
		}
	}
	return false
}

// openELF reads what the stamp needs from an ELF executable. A symbol's value
// is its address, and a section holding no file bytes (SHT_NOBITS, the zeroed
// data) or not loaded into memory is not counted.
func openELF(r io.ReaderAt) (image, error) {
	f, err := elf.NewFile(r)
	if err != nil {
		return image{}, fmt.Errorf("read the ELF file: %w", err)
	}
	syms, err := f.Symbols()
	if err != nil {
		return image{}, fmt.Errorf("read the ELF symbol table: %w", err)
	}

	img := image{r: r, order: f.ByteOrder, ptrSize: 8}
	if f.Class == elf.ELFCLASS32 {
		img.ptrSize = 4
	}
	found := false
	for _, s := range syms {
		if s.Name == stampVariable {
			img.addr, found = s.Value, true
			break
		}
	}
	if !found {
		return image{}, errNoStampSymbol
	}
	for _, s := range f.Sections {
		if s.Type == elf.SHT_NOBITS || s.Flags&elf.SHF_ALLOC == 0 {
			continue
		}
		img.sections = append(img.sections, storedSection{addr: s.Addr, size: s.Size, offset: s.Offset})
	}
	return img, nil
}

// openPE reads what the stamp needs from a PE executable. A COFF symbol's value
// is an offset into the section it names, and the pointer a string header holds
// is an absolute address, so both are taken relative to the image base. A
// section's bytes past its stored size are zeroed in memory rather than stored,
// so only the stored part of each section is counted.
func openPE(r io.ReaderAt) (image, error) {
	f, err := pe.NewFile(r)
	if err != nil {
		return image{}, fmt.Errorf("read the PE file: %w", err)
	}

	img := image{r: r, order: binary.LittleEndian}
	var base uint64
	switch oh := f.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		base, img.ptrSize = oh.ImageBase, 8
	case *pe.OptionalHeader32:
		base, img.ptrSize = uint64(oh.ImageBase), 4
	default:
		return image{}, errors.New("the PE file has no optional header, so it is not an executable")
	}

	found := false
	for _, s := range f.Symbols {
		if s.Name != stampVariable {
			continue
		}
		if s.SectionNumber < 1 || int(s.SectionNumber) > len(f.Sections) {
			return image{}, fmt.Errorf("its symbol %s names no section of the file", stampVariable)
		}
		img.addr = base + uint64(f.Sections[s.SectionNumber-1].VirtualAddress) + uint64(s.Value)
		found = true
		break
	}
	if !found {
		return image{}, errNoStampSymbol
	}
	for _, s := range f.Sections {
		stored := min(s.VirtualSize, s.Size)
		if stored == 0 {
			continue
		}
		img.sections = append(img.sections, storedSection{
			addr:   base + uint64(s.VirtualAddress),
			size:   uint64(stored),
			offset: uint64(s.Offset),
		})
	}
	return img, nil
}

// Mach-O section types whose bytes are zeroed in memory rather than stored in
// the file: the low byte of a section's flags.
const (
	machoSectionTypeMask     = 0xff
	machoZerofill            = 0x1
	machoGBZerofill          = 0xc
	machoThreadLocalZerofill = 0x12
)

// openMachO reads what the stamp needs from a single-architecture Mach-O
// executable. A symbol's value is its address, and a zero-fill section holds no
// file bytes, so it is not counted.
func openMachO(r io.ReaderAt) (image, error) {
	f, err := macho.NewFile(r)
	if err != nil {
		return image{}, fmt.Errorf("read the Mach-O file: %w", err)
	}
	if f.Symtab == nil {
		return image{}, errors.New("it has no symbol table")
	}

	img := image{r: r, order: f.ByteOrder, ptrSize: 8}
	if f.Magic == macho.Magic32 {
		img.ptrSize = 4
	}
	found := false
	for _, s := range f.Symtab.Syms {
		if s.Name == stampVariable {
			img.addr, found = s.Value, true
			break
		}
	}
	if !found {
		return image{}, errNoStampSymbol
	}
	for _, s := range f.Sections {
		switch s.Flags & machoSectionTypeMask {
		case machoZerofill, machoGBZerofill, machoThreadLocalZerofill:
			continue
		}
		img.sections = append(img.sections, storedSection{addr: s.Addr, size: s.Size, offset: uint64(s.Offset)})
	}
	return img, nil
}

// stamp reads the string header at the stamp variable's address and returns the
// bytes it points at.
//
// A variable no -X flag set is the zero string, a zero pointer and a zero
// length, and it is refused whichever way the file shows it: ELF and Mach-O
// keep it in zero-fill data the file stores no bytes for, while a PE file can
// store it as zeroes, because the section holding both kinds of data stores the
// initialised part rounded up to the file alignment, and the start of the
// zero-fill part can fall in that padding. So the zero header, not whether it
// is stored, is what marks the variable unset. A variable set to an empty value
// holds a real pointer and a zero length; it is refused too, since it carries
// no version to hold a binary to.
func (img image) stamp() (string, error) {
	header, err := img.bytesAt(img.addr, 2*img.ptrSize)
	if err != nil {
		return "", noStampError(err)
	}
	var ptr, n uint64
	if img.ptrSize == 8 {
		ptr, n = img.order.Uint64(header), img.order.Uint64(header[8:])
	} else {
		ptr, n = uint64(img.order.Uint32(header)), uint64(img.order.Uint32(header[4:]))
	}
	switch {
	case ptr == 0 && n == 0:
		return "", noStampError(errors.New("it holds the zero string"))
	case n == 0:
		return "", fmt.Errorf("%s carries no stamped version: it was stamped with an empty value", stampVariable)
	}
	version, err := img.bytesAt(ptr, n)
	if err != nil {
		return "", fmt.Errorf("the version %s points at is not in the file: %w", stampVariable, err)
	}
	return string(version), nil
}

// noStampError is the refusal for a stamp variable no -X flag set, one reason
// for every format; evidence is what the file showed.
func noStampError(evidence error) error {
	return fmt.Errorf("%s carries no stamped version: %w; a build that did not set it with -X leaves it zero", stampVariable, evidence)
}

// bytesAt returns the n bytes loaded at addr, read from the one stored section
// holding all of them.
func (img image) bytesAt(addr, n uint64) ([]byte, error) {
	for _, s := range img.sections {
		if addr < s.addr || addr-s.addr >= s.size {
			continue
		}
		within := addr - s.addr
		if n > s.size-within {
			return nil, fmt.Errorf("the %d bytes at address %#x run past the end of the section holding them", n, addr)
		}
		buf := make([]byte, n)
		if _, err := img.r.ReadAt(buf, int64(s.offset+within)); err != nil {
			return nil, fmt.Errorf("read %d bytes at address %#x: %w", n, addr, err)
		}
		return buf, nil
	}
	return nil, fmt.Errorf("no section the file stores holds address %#x", addr)
}
