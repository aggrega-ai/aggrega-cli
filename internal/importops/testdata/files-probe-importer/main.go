// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Fixture files-probe-importer plugin for the Aggrega Wasm runtime, built with the
// standard Go toolchain for GOOS=wasip1.
//
// It is an importer that reports back exactly what an import handed it, and it
// declares file patterns, so a directory import has files to read for it. Its
// describe() self-report advertises the files-probe format and one recognition
// signature: a JSON source named like *.probe.json, or a .json file in a directory
// named probe, whose top-level kind is the string FilesProbe.
//
// For every input it answers with information diagnostics and nothing else, in
// this order:
//
//   - "input <base64>" — the whole input payload it received, so a test can hold
//     the bytes the host sent against the ones it expects;
//   - "primary <base64>" — the primary source bytes;
//   - "source <name> <base64>" — one per named source, in the order received.
//
// The importer socket carries canonical CBOR in both directions, so the input is
// binary and every message carries bytes base64-encoded: a diagnostic's message is
// text, and bytes placed in it unencoded would not come back as the bytes received.
//
// It hand-writes its wire bytes rather than using the generated codec, so it
// builds inside this module with nothing but the Go toolchain and the plugin
// runtime binding, and the build that produces it watches every input it is built
// from.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o files-probe-importer.wasm ./
package main

import (
	"encoding/base64"
	"encoding/binary"
	"errors"

	pdk "github.com/extism/go-pdk"
)

// descriptor is the fixed describe() self-report: an importer of the files-probe
// format whose single export is import, declaring the two file patterns and the
// predicate its sources are recognised by, and the version a Plugin entity is
// generated at.
const descriptor = `{"abi_version":"v1beta1","plugin_type":"importer",` +
	`"description":"Report back the sources an import hands it, one diagnostic each.",` +
	`"capabilities":["import:files-probe"],"exports":[{"name":"import"}],` +
	`"importer":{"detect":[{"syntax":"json","files":["*.probe.json","probe/*.json"],` +
	`"match":[{"path":"kind","is":"string","equals":"FilesProbe"}]}]},` +
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
	input := pdk.Input()
	primary, sources, err := readInput(input)
	if err != nil {
		pdk.Output(failureOutput("files-probe-importer: the input is not an import payload"))
		return 0
	}
	messages := []string{
		"input " + base64.StdEncoding.EncodeToString(input),
		"primary " + base64.StdEncoding.EncodeToString(primary),
	}
	for _, s := range sources {
		messages = append(messages, "source "+s.name+" "+base64.StdEncoding.EncodeToString(s.bytes))
	}
	pdk.Output(diagnosticsOutput(messages))
	return 0
}

// namedSource is one named source the input carried.
type namedSource struct {
	name  string
	bytes []byte
}

// diagnosticsOutput is an import output whose one key, diagnostics, holds one
// information diagnostic per message, each a map of message and severity in
// canonical key order — the shorter key first.
func diagnosticsOutput(messages []string) []byte {
	out := appendHead(nil, majorMap, 1)
	out = appendText(out, "diagnostics")
	out = appendHead(out, majorArray, uint64(len(messages)))
	for _, m := range messages {
		out = appendHead(out, majorMap, 2)
		out = appendText(out, "message")
		out = appendText(out, m)
		out = appendText(out, "severity")
		out = appendText(out, "info")
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

// readInput reads the primary bytes and the named sources out of an import
// input map, skipping every other entry.
func readInput(data []byte) ([]byte, []namedSource, error) {
	major, n, rest, err := readHead(data)
	if err != nil || major != majorMap {
		return nil, nil, errNotAnImportInput
	}
	var primary []byte
	var sources []namedSource
	for i := uint64(0); i < n; i++ {
		var key string
		if key, rest, err = readText(rest); err != nil {
			return nil, nil, err
		}
		switch key {
		case "bytes":
			if primary, rest, err = readBytes(rest); err != nil {
				return nil, nil, err
			}
		case "sources":
			if sources, rest, err = readSources(rest); err != nil {
				return nil, nil, err
			}
		default:
			if rest, err = skip(rest); err != nil {
				return nil, nil, err
			}
		}
	}
	return primary, sources, nil
}

// readSources reads an array of named sources, each a map carrying its name and
// its bytes.
func readSources(data []byte) ([]namedSource, []byte, error) {
	major, n, rest, err := readHead(data)
	if err != nil || major != majorArray {
		return nil, nil, errNotAnImportInput
	}
	sources := make([]namedSource, 0, n)
	for i := uint64(0); i < n; i++ {
		var entries uint64
		var m byte
		if m, entries, rest, err = readHead(rest); err != nil || m != majorMap {
			return nil, nil, errNotAnImportInput
		}
		var s namedSource
		for j := uint64(0); j < entries; j++ {
			var key string
			if key, rest, err = readText(rest); err != nil {
				return nil, nil, err
			}
			switch key {
			case "name":
				if s.name, rest, err = readText(rest); err != nil {
					return nil, nil, err
				}
			case "bytes":
				if s.bytes, rest, err = readBytes(rest); err != nil {
					return nil, nil, err
				}
			default:
				if rest, err = skip(rest); err != nil {
					return nil, nil, err
				}
			}
		}
		sources = append(sources, s)
	}
	return sources, rest, nil
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
