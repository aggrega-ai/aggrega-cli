// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Fixture nonconforming-provider plugin for the Aggrega Wasm runtime, built with
// the standard Go toolchain for GOOS=wasip1.
//
// It is a provider guest that returns output the plugin contract does not admit,
// so a host refusal of a guest contract violation can be driven and the
// diagnostic it prints can be read. Each violation sits on its own export, so no
// selection is needed:
//
//   - render_node returns two output files under one path, which leaves the host
//     collecting them with two values for one key and no way to choose.
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
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o nonconforming-provider.wasm ./
package main

import (
	"encoding/json"

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
)

// providerDescriptor is the fixed describe() self-report: a provider that
// renders a sourced node and advertises both halves of the cross-boundary link
// capability, which is what routes a link crossing to it at all.
const providerDescriptor = `{` +
	`"abi_version":"v1alpha1",` +
	`"plugin_type":"provider",` +
	`"capabilities":["` + capability + `"],` +
	`"description":"Returns provider output the contract does not admit, to drive a host refusal.",` +
	`"exports":[` +
	`{"name":"render_node","input_pattern":"b","description":"returns two output files under one path"},` +
	`{"name":"link_publish","input_pattern":"b","description":"returns wiring naming a file the group did not emit"},` +
	`{"name":"link_resolve","input_pattern":"b","description":"returns no wiring"}` +
	`],` +
	// cross_boundary_linking is declared because the load gate requires it of any
	// guest exporting both link halves, and refuses the guest outright otherwise.
	// It is a CONFORMING declaration on purpose: the fixture must load and run to
	// be able to violate anything, so every part of its self-report is honest and
	// only the payloads it returns are not.
	`"contract":{"input_kinds":{"sourced":true},"boundary_assembly":"none",` +
	`"reusable_instance":true,"cross_boundary_linking":true}` +
	`}`

// renderedFile mirrors the wire shape of one returned output file. Content is
// []byte so the standard encoder base64-encodes it, which is the encoding the
// wire declares for those bytes.
type renderedFile struct {
	Path    string `json:"path"`
	Content []byte `json:"content"`
}

// renderNodeOutput is the sliver of the render reply this guest returns: the
// files alone. Every other field is optional and absent.
type renderNodeOutput struct {
	Files []renderedFile `json:"files"`
}

// linkPatch mirrors the wire shape of one per-file structural patch. Ops is
// empty: the violation is the file the patch NAMES, and it is refused before any
// operation would be applied, so carrying operations would add nothing the test
// could attribute.
type linkPatch struct {
	File string           `json:"file"`
	Ops  []map[string]any `json:"ops"`
}

// linkPublishOutput is the sliver of the publish reply this guest returns: the
// published handle, and the wiring naming a file nothing emitted.
type linkPublishOutput struct {
	Handle  map[string]string `json:"handle"`
	Patches []linkPatch       `json:"patches"`
}

// linkResolveOutput is the sliver of the resolve reply this guest returns. The
// patch list is required on the wire, so it is present and empty rather than
// omitted.
type linkResolveOutput struct {
	Patches []linkPatch `json:"patches"`
}

func output(v any) int32 {
	raw, err := json.Marshal(v)
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	pdk.Output(raw)
	return 0
}

//go:wasmexport describe
func describe() int32 {
	pdk.OutputString(providerDescriptor)
	return 0
}

//go:wasmexport render_node
func render_node() int32 {
	// Two entries, one path. The bytes differ so a host that silently took one
	// of them would produce output a test could tell apart from the other.
	return output(renderNodeOutput{Files: []renderedFile{
		{Path: duplicatedPath, Content: []byte("kind: first\n")},
		{Path: duplicatedPath, Content: []byte("kind: second\n")},
	}})
}

//go:wasmexport link_publish
func link_publish() int32 {
	return output(linkPublishOutput{
		Handle:  map[string]string{"pointer": "nonconforming-pointer"},
		Patches: []linkPatch{{File: absentFile, Ops: []map[string]any{}}},
	})
}

//go:wasmexport link_resolve
func link_resolve() int32 {
	return output(linkResolveOutput{Patches: []linkPatch{}})
}

func main() {}
