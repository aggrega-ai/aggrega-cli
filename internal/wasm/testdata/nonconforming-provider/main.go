// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Fixture nonconforming-provider plugin for the Aggrega Wasm runtime, built with
// the standard Go toolchain for GOOS=wasip1.
//
// It is a provider guest that returns output the plugin contract does not admit,
// so a host refusal of a guest contract violation can be driven and the
// diagnostic it prints can be read. Each violation sits on its own export or, for
// the two render_node carries, on its own crossing of that export, so the input
// the host hands over is the whole selection:
//
//   - render_node, handed a source to render, returns two output files under one
//     path, which leaves the host collecting them with two values for one key and
//     no way to choose.
//   - render_node, handed a workload to synthesize, returns one ordinary file and
//     one render fact whose value is not a list. The node's id names the kind of
//     value reported — "object", "string", "number", "boolean", "null" — and any
//     other id gets a list, the conforming answer, so the refusal can be driven for
//     every kind and its absence shown on the same guest.
//   - link_publish returns artifact wiring naming an output file the dispatch
//     group it applies to did not emit, which leaves the pass that carves that
//     file nothing to carve.
//
// link_resolve returns no wiring at all. That is deliberate: it keeps the
// publish-side violation the only thing that can halt the link pass, so the halt
// is attributable to the file the publish half named rather than to whichever of
// the two halves ran first.
//
// The rendered bytes are ordinary and valid YAML. Only the SHAPE of the reply is
// wrong, which is what makes the fixture a test of the contract rather than of
// the YAML reader underneath it.
//
// The render socket carries canonical CBOR in both directions, and every reply
// here is well-formed in that encoding and declares only fields its shape has:
// what is wrong is what those fields say. The guest hand-writes its wire bytes
// rather than using the generated codec, so it builds inside this module with
// nothing but the Go toolchain and the plugin runtime binding: it reads the two
// things it needs — the node's id and whether a workload projection is present —
// out of the input map, and writes each output map itself.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o nonconforming-provider.wasm ./
package main

import (
	"errors"

	pdk "github.com/extism/go-pdk"
)

// The capability this guest serves and the output path it duplicates. The path
// is duplicated by being returned twice rather than by any property of the path
// itself, so the name carries no meaning beyond being the same both times.
const (
	capability     = "nonconforming"
	duplicatedPath = "manifest.yaml"

	// absentFile is the file link_publish claims to wire. Nothing emits it, and
	// the name says so, so a reader of the halt is not left wondering whether a
	// real file was meant.
	absentFile = "never-emitted.yaml"

	// synthesizedPath is the one ordinary file a synthesis returns, and factName
	// the render fact reported beside it.
	synthesizedPath = "workload.yaml"
	factName        = "pod_labels"
)

// factValues is the value reported under factName for each node id that asks for
// a kind other than a list. The bytes are written here rather than marshalled, so
// what crosses is exactly the kind the id names.
var factValues = map[string]string{
	"object":  `{"app":"api"}`,
	"string":  `"api"`,
	"number":  `3`,
	"boolean": `true`,
	"null":    `null`,
}

// conformingFactValue is reported for any other node id: a list, which the
// contract requires of every fact's value.
const conformingFactValue = `[{"app":"api"}]`

// providerDescriptor is the fixed describe() self-report: a provider that
// renders a sourced node and advertises both halves of the cross-boundary link
// capability, which is what routes a link crossing to it at all.
const providerDescriptor = `{` +
	`"abi_version":"v1alpha1",` +
	`"plugin_type":"provider",` +
	`"capabilities":["` + capability + `"],` +
	`"description":"Returns provider output the contract does not admit, to drive a host refusal.",` +
	`"exports":[` +
	`{"name":"render_node","input_pattern":"b","description":"returns two output files under one path, or a render fact whose value is not a list"},` +
	`{"name":"link_publish","input_pattern":"b","description":"returns wiring naming a file the group did not emit"},` +
	`{"name":"link_resolve","input_pattern":"b","description":"returns no wiring"}` +
	`],` +
	// cross_boundary_linking is declared because the load gate requires it of any
	// guest exporting both link halves, and refuses the guest outright otherwise.
	// It is a CONFORMING declaration on purpose: the fixture must load and run to
	// be able to violate anything, so every part of its self-report is honest and
	// only the payloads it returns are not.
	`"contract":{"input_kinds":{"sourced":true,"synthesized":true},"boundary_assembly":"none",` +
	`"reusable_instance":true,"cross_boundary_linking":true}` +
	`}`

// renderedFile is one returned output file: its path, and its bytes, which cross
// as a CBOR byte string.
type renderedFile struct {
	Path    string
	Content []byte
}

// renderFact is one render fact. Value is the raw JSON this guest wrote, carried
// as a CBOR byte string of exactly those bytes, so its kind is exactly what the
// guest chose.
type renderFact struct {
	Name  string
	Value string
}

// renderNodeOutput writes the sliver of the render reply this guest returns: the
// files, and the render facts a synthesis reports, as a CBOR map in canonical key
// order — the shorter key first. Every other field is optional and absent.
func renderNodeOutput(files []renderedFile, facts []renderFact) []byte {
	pairs := uint64(1)
	if len(facts) > 0 {
		pairs++
	}
	out := appendHead(nil, majorMap, pairs)
	out = appendText(out, "files")
	out = appendHead(out, majorArray, uint64(len(files)))
	for _, f := range files {
		out = appendHead(out, majorMap, 2)
		out = appendText(out, "path")
		out = appendText(out, f.Path)
		out = appendText(out, "content")
		out = appendBytes(out, f.Content)
	}
	if len(facts) > 0 {
		out = appendText(out, "render_facts")
		out = appendHead(out, majorArray, uint64(len(facts)))
		for _, f := range facts {
			out = appendHead(out, majorMap, 2)
			out = appendText(out, "name")
			out = appendText(out, f.Name)
			out = appendText(out, "value")
			out = appendBytes(out, []byte(f.Value))
		}
	}
	return out
}

// linkPatchNaming writes one per-file structural patch naming file. Its
// operation list is empty: the violation is the file the patch NAMES, and it is
// refused before any operation would be applied, so carrying operations would
// add nothing the test could attribute.
func linkPatchNaming(dst []byte, file string) []byte {
	dst = appendHead(dst, majorMap, 2)
	dst = appendText(dst, "ops")
	dst = appendHead(dst, majorArray, 0)
	dst = appendText(dst, "file")
	return appendText(dst, file)
}

// renderNodeIdentity reads what the render input tells this guest: the node's
// id, and whether the host handed it a workload to synthesize. Every other entry
// of the input map is skipped.
func renderNodeIdentity(data []byte) (id string, synthesis bool, err error) {
	major, n, rest, err := readHead(data)
	if err != nil || major != majorMap {
		return "", false, errNotARenderInput
	}
	for i := uint64(0); i < n; i++ {
		var key string
		if key, rest, err = readText(rest); err != nil {
			return "", false, err
		}
		switch key {
		case "node":
			if id, rest, err = nodeID(rest); err != nil {
				return "", false, err
			}
		case "workload_projection":
			synthesis = true
			fallthrough
		default:
			if rest, err = skip(rest); err != nil {
				return "", false, err
			}
		}
	}
	return id, synthesis, nil
}

// nodeID reads the id text out of the node projection map, skipping every other
// entry, and returns the bytes after the map.
func nodeID(data []byte) (string, []byte, error) {
	major, n, rest, err := readHead(data)
	if err != nil || major != majorMap {
		return "", nil, errNotARenderInput
	}
	var id string
	for i := uint64(0); i < n; i++ {
		var key string
		if key, rest, err = readText(rest); err != nil {
			return "", nil, err
		}
		if key == "id" {
			if id, rest, err = readText(rest); err != nil {
				return "", nil, err
			}
			continue
		}
		if rest, err = skip(rest); err != nil {
			return "", nil, err
		}
	}
	return id, rest, nil
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

var errNotARenderInput = errors.New("nonconforming-provider: the input is not a render_node payload")

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

// appendBytes appends b as a CBOR byte string.
func appendBytes(dst, b []byte) []byte {
	return append(appendHead(dst, majorBytes, uint64(len(b))), b...)
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

//go:wasmexport describe
func describe() int32 {
	pdk.OutputString(providerDescriptor)
	return 0
}

//go:wasmexport render_node
func render_node() int32 {
	id, synthesis, err := renderNodeIdentity(pdk.Input())
	if err != nil {
		pdk.SetError(err)
		return 1
	}

	// A synthesis: one ordinary file, and one render fact whose value is the
	// kind the node's id names.
	if synthesis {
		value, named := factValues[id]
		if !named {
			value = conformingFactValue
		}
		pdk.Output(renderNodeOutput(
			[]renderedFile{{Path: synthesizedPath, Content: []byte("kind: Workload\n")}},
			[]renderFact{{Name: factName, Value: value}},
		))
		return 0
	}

	// Two entries, one path. The bytes differ so a host that silently took one
	// of them would produce output a test could tell apart from the other.
	pdk.Output(renderNodeOutput([]renderedFile{
		{Path: duplicatedPath, Content: []byte("kind: first\n")},
		{Path: duplicatedPath, Content: []byte("kind: second\n")},
	}, nil))
	return 0
}

// link_publish returns the published handle, and wiring naming a file nothing
// emitted, as a CBOR map in canonical key order.
//
//go:wasmexport link_publish
func link_publish() int32 {
	out := appendHead(nil, majorMap, 2)
	out = appendText(out, "handle")
	out = appendHead(out, majorMap, 1)
	out = appendText(out, "pointer")
	out = appendText(out, "nonconforming-pointer")
	out = appendText(out, "patches")
	out = appendHead(out, majorArray, 1)
	pdk.Output(linkPatchNaming(out, absentFile))
	return 0
}

// link_resolve returns no wiring. The patch list is required on the wire, so it
// is present and empty rather than omitted.
//
//go:wasmexport link_resolve
func link_resolve() int32 {
	out := appendHead(nil, majorMap, 1)
	out = appendText(out, "patches")
	pdk.Output(appendHead(out, majorArray, 0))
	return 0
}

func main() {}
