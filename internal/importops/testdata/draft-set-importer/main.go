// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Fixture draft-set-importer plugin for the Aggrega Wasm runtime, built with the
// standard Go toolchain for GOOS=wasip1.
//
// It is an importer that drafts exactly the files its source spells out, so a
// test chooses the draft set an import composes from — an Aggregate, a Release,
// anything at all — without depending on how a real importer reads a real
// format. Its describe() self-report advertises the draft-set format and
// declares no recognition signature, so an import of it names the format.
//
// The source is text. A line opening with "=== " starts a drafted file, the rest
// of the line naming its path, and every line after it up to the next such line
// is that file's content, line endings included. Anything before the first such
// line is ignored. The files are drafted in the order the source spells them.
//
// It hand-writes its wire bytes rather than using the generated codec, so it
// builds inside this module with nothing but the Go toolchain and the plugin
// runtime binding, and the build that produces it watches every input it is built
// from.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o draft-set-importer.wasm ./
package main

import (
	"encoding/binary"
	"errors"
	"strings"

	pdk "github.com/extism/go-pdk"
)

// descriptor is the fixed describe() self-report: an importer of the draft-set
// format whose single export is import, and the version a Plugin entity is
// generated at.
const descriptor = `{"abi_version":"v1beta1","plugin_type":"importer",` +
	`"description":"Draft exactly the files the source spells out, in its order.",` +
	`"capabilities":["import:draft-set"],"exports":[{"name":"import"}],` +
	`"metadata":{"version":"1.0.0"}}`

//go:wasmexport describe
func describe() int32 {
	pdk.OutputString(descriptor)
	return 0
}

// importArtifact is the import export. The Go function is named differently from
// the export because import is a Go keyword; the directive carries the name the
// host dispatches.
//
//go:wasmexport import
func importArtifact() int32 {
	primary, err := readPrimary(pdk.Input())
	if err != nil {
		pdk.Output(failureOutput("draft-set-importer: the input is not an import payload"))
		return 0
	}
	pdk.Output(filesOutput(splitFiles(string(primary))))
	return 0
}

// draftedFile is one file the source spells out.
type draftedFile struct {
	path    string
	content string
}

// filePrefix opens the line that starts a drafted file.
const filePrefix = "=== "

// splitFiles reads the drafted files out of the source, in its order.
func splitFiles(source string) []draftedFile {
	var files []draftedFile
	var current *draftedFile
	for source != "" {
		line := source
		if i := strings.IndexByte(source, '\n'); i >= 0 {
			line = source[:i+1]
		}
		source = source[len(line):]
		if strings.HasPrefix(line, filePrefix) {
			files = append(files, draftedFile{path: strings.TrimRight(strings.TrimPrefix(line, filePrefix), "\r\n")})
			current = &files[len(files)-1]
			continue
		}
		if current != nil {
			current.content += line
		}
	}
	return files
}

// filesOutput is an import output whose one key, files, holds one scaffold file
// per drafted file, each a map of path and content in canonical key order — the
// shorter key first.
func filesOutput(files []draftedFile) []byte {
	out := appendHead(nil, majorMap, 1)
	out = appendText(out, "files")
	out = appendHead(out, majorArray, uint64(len(files)))
	for _, f := range files {
		out = appendHead(out, majorMap, 2)
		out = appendText(out, "path")
		out = appendText(out, f.path)
		out = appendText(out, "content")
		out = appendText(out, f.content)
	}
	return out
}

// failureOutput is the answer for an input this guest could not read: the
// output's error field, which the host surfaces as a failed invocation.
func failureOutput(message string) []byte {
	out := appendHead(nil, majorMap, 1)
	out = appendText(out, "error")
	return appendText(out, message)
}

// The CBOR major types this guest reads or writes.
const (
	majorUint   = 0
	majorNegint = 1
	majorBytes  = 2
	majorText   = 3
	majorArray  = 4
	majorMap    = 5
	majorSimple = 7
)

// appendHead appends a CBOR item head of the given major type and argument in
// its shortest form.
func appendHead(dst []byte, major byte, n uint64) []byte {
	m := major << 5
	switch {
	case n < 24:
		return append(dst, m|byte(n))
	case n <= 0xff:
		return append(dst, m|24, byte(n))
	case n <= 0xffff:
		return binary.BigEndian.AppendUint16(append(dst, m|25), uint16(n))
	case n <= 0xffffffff:
		return binary.BigEndian.AppendUint32(append(dst, m|26), uint32(n))
	default:
		return binary.BigEndian.AppendUint64(append(dst, m|27), n)
	}
}

// appendText appends s as a CBOR text string.
func appendText(dst []byte, s string) []byte {
	return append(appendHead(dst, majorText, uint64(len(s))), s...)
}

var errNotAnImportInput = errors.New("not an import input")

// readPrimary reads the primary bytes out of an import input map, skipping every
// other entry.
func readPrimary(data []byte) ([]byte, error) {
	major, n, rest, err := readHead(data)
	if err != nil || major != majorMap {
		return nil, errNotAnImportInput
	}
	var primary []byte
	for i := uint64(0); i < n; i++ {
		var key string
		if key, rest, err = readText(rest); err != nil {
			return nil, err
		}
		if key == "bytes" {
			if primary, rest, err = readBytes(rest); err != nil {
				return nil, err
			}
			continue
		}
		if rest, err = skip(rest); err != nil {
			return nil, err
		}
	}
	return primary, nil
}

// readHead reads one definite-length item head and returns its major type, its
// argument and the bytes after the head.
func readHead(data []byte) (byte, uint64, []byte, error) {
	if len(data) == 0 {
		return 0, 0, nil, errNotAnImportInput
	}
	major, info := data[0]>>5, data[0]&0x1f
	data = data[1:]
	var size int
	switch {
	case info < 24:
		return major, uint64(info), data, nil
	case info == 24:
		size = 1
	case info == 25:
		size = 2
	case info == 26:
		size = 4
	case info == 27:
		size = 8
	default:
		return 0, 0, nil, errNotAnImportInput
	}
	if len(data) < size {
		return 0, 0, nil, errNotAnImportInput
	}
	var n uint64
	for _, b := range data[:size] {
		n = n<<8 | uint64(b)
	}
	return major, n, data[size:], nil
}

// readText reads one CBOR text string.
func readText(data []byte) (string, []byte, error) {
	major, n, rest, err := readHead(data)
	if err != nil || major != majorText || uint64(len(rest)) < n {
		return "", nil, errNotAnImportInput
	}
	return string(rest[:n]), rest[n:], nil
}

// readBytes reads one CBOR byte string.
func readBytes(data []byte) ([]byte, []byte, error) {
	major, n, rest, err := readHead(data)
	if err != nil || major != majorBytes || uint64(len(rest)) < n {
		return nil, nil, errNotAnImportInput
	}
	return rest[:n], rest[n:], nil
}

// skip steps over one complete item: an integer, a string of either kind, an
// array, a map, or a simple value — every item an import input carries.
func skip(data []byte) ([]byte, error) {
	major, n, rest, err := readHead(data)
	if err != nil {
		return nil, err
	}
	switch major {
	case majorUint, majorNegint, majorSimple:
		return rest, nil
	case majorBytes, majorText:
		if uint64(len(rest)) < n {
			return nil, errNotAnImportInput
		}
		return rest[n:], nil
	case majorArray, majorMap:
		items := n
		if major == majorMap {
			items = 2 * n
		}
		for i := uint64(0); i < items; i++ {
			if rest, err = skip(rest); err != nil {
				return nil, err
			}
		}
		return rest, nil
	default:
		return nil, errNotAnImportInput
	}
}

func main() {}
