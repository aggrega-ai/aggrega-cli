// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build wasip1

// Fixture payload-echo-linter plugin for the Aggrega Wasm runtime, built with the
// standard Go toolchain for GOOS=wasip1.
//
// It is a linter guest that reports back exactly what it was handed. For every
// input it returns one advisory finding: its rule is the rule the input names,
// and its message is the input's bytes, base64-encoded. A test dispatching it
// through the host's own linter dispatcher therefore reads, in the finding it
// gets back, the payload this guest received — not a structure the host built on
// the way to encoding one.
//
// The linter socket carries canonical CBOR in both directions, so the input is
// binary and the message carries it base64-encoded: a finding's message is text,
// and bytes placed in it unencoded would not come back as the bytes received.
//
// It hand-writes its wire bytes rather than using the generated codec, so it
// builds inside this module with nothing but the Go toolchain and the plugin
// runtime binding: it reads the one key it needs, rule_id, out of the input map,
// and writes the output map itself.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o payload-echo-linter.wasm ./
package main

import (
	"encoding/base64"
	"encoding/binary"
	"errors"

	pdk "github.com/extism/go-pdk"
)

// descriptor is the fixed describe() self-report: a linter plugin whose single
// export is the socket's default rule-evaluation entry point.
const descriptor = `{"abi_version":"v1beta1","plugin_type":"linter","exports":[{"name":"lint_rules"}]}`

//go:wasmexport describe
func describe() int32 {
	pdk.OutputString(descriptor)
	return 0
}

// echoOutput is the one finding this guest returns for input: the rule the input
// names, at the advisory severity, carrying the input itself, base64-encoded, as
// its message. The output is a CBOR map whose one key, findings, holds a
// one-element array, and the finding is a CBOR map in the shared diagnostic's
// shape, its keys in canonical order — the shorter key first, then bytewise.
func echoOutput(input []byte) ([]byte, error) {
	ruleID, err := ruleIDOf(input)
	if err != nil {
		return nil, err
	}
	out := appendHead(nil, majorMap, 1)
	out = appendText(out, "findings")
	out = appendHead(out, majorArray, 1)
	out = appendHead(out, majorMap, 3)
	out = appendText(out, "message")
	out = appendText(out, base64.StdEncoding.EncodeToString(input))
	out = appendText(out, "rule_id")
	out = appendText(out, ruleID)
	out = appendText(out, "severity")
	return appendText(out, "warn"), nil
}

// failureOutput is the answer for an input this guest could not read: the
// output's error field, which the host surfaces as a failed invocation, beside an
// empty finding list, so an unreadable input never reads as a run that found
// nothing. Keys are written in canonical order, the shorter key first.
func failureOutput(message string) []byte {
	out := appendHead(nil, majorMap, 2)
	out = appendText(out, "error")
	out = appendText(out, message)
	out = appendText(out, "findings")
	return appendHead(out, majorArray, 0)
}

//go:wasmexport lint_rules
func lintRules() int32 {
	out, err := echoOutput(pdk.Input())
	if err != nil {
		pdk.Output(failureOutput("payload-echo-linter: the input is not a linter payload"))
		return 0
	}
	pdk.Output(out)
	return 0
}

// The CBOR major types this guest reads or writes.
const (
	majorUint  = 0
	majorBytes = 2
	majorText  = 3
	majorArray = 4
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

var errNotALinterInput = errors.New("not a linter input")

// ruleIDOf reads the rule_id text out of a CBOR map, skipping every other entry.
func ruleIDOf(data []byte) (string, error) {
	major, n, rest, err := readHead(data)
	if err != nil || major != majorMap {
		return "", errNotALinterInput
	}
	for i := uint64(0); i < n; i++ {
		var key string
		key, rest, err = readText(rest)
		if err != nil {
			return "", err
		}
		if key == "rule_id" {
			id, _, err := readText(rest)
			return id, err
		}
		if rest, err = skip(rest); err != nil {
			return "", err
		}
	}
	return "", errNotALinterInput
}

// readHead reads one definite-length item head and returns its major type, its
// argument and the bytes after the head.
func readHead(data []byte) (byte, uint64, []byte, error) {
	if len(data) == 0 {
		return 0, 0, nil, errNotALinterInput
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
		return 0, 0, nil, errNotALinterInput
	}
	if len(data) < size {
		return 0, 0, nil, errNotALinterInput
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
		return "", nil, errNotALinterInput
	}
	return string(rest[:n]), rest[n:], nil
}

// skip steps over one complete item: a string of either kind, an integer, an
// array or a map. That covers every entry sorting before rule_id in a linter
// input's canonical key order, so ruleIDOf never reaches the configuration's
// booleans, which sort after it.
func skip(data []byte) ([]byte, error) {
	major, n, rest, err := readHead(data)
	if err != nil {
		return nil, err
	}
	switch major {
	case majorUint, 1:
		return rest, nil
	case majorBytes, majorText:
		if uint64(len(rest)) < n {
			return nil, errNotALinterInput
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
		return nil, errNotALinterInput
	}
}

func main() {}
