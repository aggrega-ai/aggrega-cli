// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build wasip1

// Fixture planted-json-parser plugin for the Aggrega Wasm runtime, built with the
// standard Go toolchain for GOOS=wasip1.
//
// It is a parser advertising the json syntax that reads nothing: whatever source
// it is handed, its parse export answers one fixed tree, so a test can tell from
// what an import did with a source whether this parser read it or another parser
// for the same syntax did. The tree has the shape a flow export keeps its graph
// flat in — arrays at nodes and edges and nothing at data.nodes — and its one
// node is of the type plantedMarkerParser, so a source this parser read is
// recognised by the signature that asks for that shape, and drafted as one
// component named after that type, whatever the source really held.
//
// The parser socket carries canonical CBOR in both directions, so the answer is a
// map with the one key tree, holding the tree's JSON document as a byte string.
// The input is never read: the answer is the same for every source.
//
// It hand-writes its wire bytes rather than using the generated codec, so it
// builds inside this module with nothing but the Go toolchain and the plugin
// runtime binding, and the build that produces it watches every input it is built
// from.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o planted-json-parser.wasm ./
package main

import (
	"encoding/binary"

	pdk "github.com/extism/go-pdk"
)

// descriptor is the fixed describe() self-report: a parser of the json syntax
// whose single export is parse, and the version a Plugin entity is generated at.
const descriptor = `{"abi_version":"v1beta1","plugin_type":"parser",` +
	`"description":"Answer one fixed tree for any source, so a test can tell which parser read it.",` +
	`"capabilities":["parse:json"],"exports":[{"name":"parse"}],` +
	`"metadata":{"version":"1.0.0"}}`

// plantedTree is the one tree every parse answers, as its JSON document.
const plantedTree = `{"edges":[],"nodes":[{"data":{"name":"plantedMarkerParser"},"id":"planted-marker",` +
	`"type":"customNode"}]}`

//go:wasmexport describe
func describe() int32 {
	pdk.OutputString(descriptor)
	return 0
}

//go:wasmexport parse
func parse() int32 {
	out := appendHead(nil, majorMap, 1)
	out = appendText(out, "tree")
	out = appendHead(out, majorBytes, uint64(len(plantedTree)))
	out = append(out, plantedTree...)
	pdk.Output(out)
	return 0
}

// The CBOR major types this guest writes.
const (
	majorBytes = 2
	majorText  = 3
	majorMap   = 5
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

func main() {}
