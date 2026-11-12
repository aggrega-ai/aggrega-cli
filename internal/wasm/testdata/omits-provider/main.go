// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build wasip1

// Fixture omits-provider plugin for the Aggrega Wasm runtime, built with the
// standard Go toolchain for GOOS=wasip1.
//
// It is a provider guest whose self-report claims to honour omit entries, so the
// load gate's comparison of that claim against a Plugin entity, and the entity
// generated from the guest's describe(), can be driven with a real guest rather
// than a hand-built descriptor.
//
// The claim is honest. render_node renders a fixed set of two units, one file
// each, and leaves out every unit an omit entry it was handed matches by kind and
// name. The units sit in no namespace, so an entry naming one matches nothing. It
// reports one count per entry, in the order the entries arrived, and reports none
// when it was handed none.
//
// A render handed entries also writes one more file, received-omits.txt, saying for
// each entry, in the order it arrived, whether the input marked it sensitive. That
// is what lets a build read the mark as this guest received it, rather than as the
// host meant to send it. The file names no field of any entry: a marked entry's
// fields are kept out of every text the guest returns, this one included.
//
// The render socket carries canonical CBOR in both directions. The guest
// hand-writes its wire bytes rather than using the generated codec, so it builds
// inside this module with nothing but the Go toolchain and the plugin runtime
// binding: it reads the one key it needs, omits, out of the input map, and writes
// the output map itself.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o omits-provider.wasm ./
package main

import (
	"errors"
	"fmt"
	"strings"

	pdk "github.com/extism/go-pdk"
)

// providerDescriptor is the fixed describe() self-report: a stateless provider
// that renders a sourced node and honours omit entries. The build-provenance
// version is stamped so a Plugin entity can be generated from it.
const providerDescriptor = `{` +
	`"abi_version":"v1beta1",` +
	`"plugin_type":"provider",` +
	`"capabilities":["omits-example"],` +
	`"description":"Renders two fixed units and leaves out the ones its omit entries name.",` +
	`"exports":[{"name":"render_node","input_pattern":"b","description":"renders two fixed units, less the ones an omit entry names"}],` +
	`"contract":{"input_kinds":{"sourced":true,"synthesized":false,"artifacts_standalone":false},"boundary_assembly":"none",` +
	`"reusable_instance":true,"omits":true},` +
	`"metadata":{"version":"1.0.0"}` +
	`}`

// unit is one thing this guest renders: the kind and name an omit entry selects
// it by, and the file it is written to.
type unit struct {
	kind, name, path, body string
}

// units is the fixed set every render produces before any entry is applied.
var units = []unit{
	{kind: "ConfigMap", name: "settings", path: "settings.yaml", body: "kind: ConfigMap\nname: settings\n"},
	{kind: "Secret", name: "token", path: "token.yaml", body: "kind: Secret\nname: token\n"},
}

// omitEntry is one entry the host hands over: the fields an entry selects a unit
// by, and whether the input marked it sensitive.
type omitEntry struct {
	Kind      string
	Name      string
	Namespace string
	Sensitive bool
}

// receivedOmitsPath is the file a render handed entries writes its account of
// their marks to.
const receivedOmitsPath = "received-omits.txt"

// renderedFile is one returned output file: its path, and its bytes, which cross
// as a CBOR byte string.
type renderedFile struct {
	Path    string
	Content []byte
}

// renderNodeOutput is the sliver of the render reply this guest returns.
type renderNodeOutput struct {
	Files   []renderedFile
	Omitted []int
}

// encode writes the reply as a CBOR map in canonical key order — the shorter key
// first — with omitted left out when no entry was handed over.
func (o renderNodeOutput) encode() []byte {
	pairs := uint64(1)
	if len(o.Omitted) > 0 {
		pairs++
	}
	out := appendHead(nil, majorMap, pairs)
	out = appendText(out, "files")
	out = appendHead(out, majorArray, uint64(len(o.Files)))
	for _, f := range o.Files {
		out = appendHead(out, majorMap, 2)
		out = appendText(out, "path")
		out = appendText(out, f.Path)
		out = appendText(out, "content")
		out = append(appendHead(out, majorBytes, uint64(len(f.Content))), f.Content...)
	}
	if len(o.Omitted) > 0 {
		out = appendText(out, "omitted")
		out = appendHead(out, majorArray, uint64(len(o.Omitted)))
		for _, n := range o.Omitted {
			out = appendHead(out, majorUint, uint64(n))
		}
	}
	return out
}

// matches reports whether an entry selects a unit. The units sit in no
// namespace, so an entry naming one selects none of them.
func matches(e omitEntry, u unit) bool {
	return e.Namespace == "" && e.Kind == u.kind && e.Name == u.name
}

//go:wasmexport describe
func describe() int32 {
	pdk.OutputString(providerDescriptor)
	return 0
}

//go:wasmexport render_node
func render_node() int32 {
	omits, err := omitsOf(pdk.Input())
	if err != nil {
		pdk.SetError(err)
		return 1
	}

	out := renderNodeOutput{Files: []renderedFile{}}
	if len(omits) > 0 {
		out.Omitted = make([]int, len(omits))
	}
	for _, u := range units {
		left := false
		for i, e := range omits {
			if matches(e, u) {
				// Every matching entry counts the unit, so a unit two entries name
				// is counted under both.
				out.Omitted[i]++
				left = true
			}
		}
		if !left {
			out.Files = append(out.Files, renderedFile{Path: u.path, Content: []byte(u.body)})
		}
	}
	if len(omits) > 0 {
		var received strings.Builder
		for i, e := range omits {
			fmt.Fprintf(&received, "omits[%d] sensitive=%t\n", i, e.Sensitive)
		}
		out.Files = append(out.Files, renderedFile{Path: receivedOmitsPath, Content: []byte(received.String())})
	}

	pdk.Output(out.encode())
	return 0
}

// The CBOR major types this guest reads or writes.
const (
	majorUint   = 0
	majorNeg    = 1
	majorBytes  = 2
	majorText   = 3
	majorArray  = 4
	majorMap    = 5
	majorSimple = 7
)

var errNotARenderInput = errors.New("omits-provider: the input is not a render_node payload")

// omitsOf reads the omits list out of a render_node input map, skipping every
// other entry. An input carrying no omits key carries no entries.
func omitsOf(data []byte) ([]omitEntry, error) {
	major, n, rest, err := readHead(data)
	if err != nil || major != majorMap {
		return nil, errNotARenderInput
	}
	for i := uint64(0); i < n; i++ {
		var key string
		if key, rest, err = readText(rest); err != nil {
			return nil, err
		}
		if key != "omits" {
			if rest, err = skip(rest); err != nil {
				return nil, err
			}
			continue
		}
		var count uint64
		if major, count, rest, err = readHead(rest); err != nil || major != majorArray {
			return nil, errNotARenderInput
		}
		entries := make([]omitEntry, 0, count)
		for j := uint64(0); j < count; j++ {
			var e omitEntry
			if e, rest, err = readOmitEntry(rest); err != nil {
				return nil, err
			}
			entries = append(entries, e)
		}
		return entries, nil
	}
	return nil, nil
}

// readOmitEntry reads one omit entry map: its text fields and its sensitivity
// mark, skipping any key it does not use.
func readOmitEntry(data []byte) (omitEntry, []byte, error) {
	var e omitEntry
	major, n, rest, err := readHead(data)
	if err != nil || major != majorMap {
		return e, nil, errNotARenderInput
	}
	for i := uint64(0); i < n; i++ {
		var key string
		if key, rest, err = readText(rest); err != nil {
			return e, nil, err
		}
		switch key {
		case "kind":
			e.Kind, rest, err = readText(rest)
		case "name":
			e.Name, rest, err = readText(rest)
		case "namespace":
			e.Namespace, rest, err = readText(rest)
		case "sensitive":
			if len(rest) == 0 || (rest[0] != 0xf4 && rest[0] != 0xf5) {
				return e, nil, errNotARenderInput
			}
			e.Sensitive, rest = rest[0] == 0xf5, rest[1:]
		default:
			rest, err = skip(rest)
		}
		if err != nil {
			return e, nil, err
		}
	}
	return e, rest, nil
}

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
		return append(dst, m|25, byte(n>>8), byte(n))
	case n <= 0xffffffff:
		return append(dst, m|26, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	default:
		return append(dst, m|27, byte(n>>56), byte(n>>48), byte(n>>40), byte(n>>32),
			byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
}

// appendText appends s as a CBOR text string.
func appendText(dst []byte, s string) []byte {
	return append(appendHead(dst, majorText, uint64(len(s))), s...)
}

// readHead reads one definite-length item head and returns its major type, its
// argument and the bytes after the head. For a simple value or a float the
// argument is the additional information itself.
func readHead(data []byte) (byte, uint64, []byte, error) {
	if len(data) == 0 {
		return 0, 0, nil, errNotARenderInput
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
		return 0, 0, nil, errNotARenderInput
	}
	if len(data) < size {
		return 0, 0, nil, errNotARenderInput
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
		return "", nil, errNotARenderInput
	}
	return string(rest[:n]), rest[n:], nil
}

// skip steps over one complete item of any kind a render_node input carries.
func skip(data []byte) ([]byte, error) {
	major, n, rest, err := readHead(data)
	if err != nil {
		return nil, err
	}
	switch major {
	case majorUint, majorNeg, majorSimple:
		return rest, nil
	case majorBytes, majorText:
		if uint64(len(rest)) < n {
			return nil, errNotARenderInput
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
		return nil, errNotARenderInput
	}
}

func main() {}
