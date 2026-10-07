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

// renderedFile mirrors the wire shape of one returned output file. Content is
// []byte so the standard encoder base64-encodes it, which is the encoding the
// wire declares for those bytes.
type renderedFile struct {
	Path    string `json:"path"`
	Content []byte `json:"content"`
}

// renderNodeOutput is the sliver of the render reply this guest returns: the
// files, and the render facts a synthesis reports. Every other field is optional
// and absent.
type renderNodeOutput struct {
	Files       []renderedFile `json:"files"`
	RenderFacts []renderFact   `json:"render_facts,omitempty"`
}

// renderFact mirrors the wire shape of one render fact. Value is carried as the
// raw JSON this guest wrote, so its kind is exactly what the guest chose.
type renderFact struct {
	Name  string          `json:"name"`
	Value json.RawMessage `json:"value"`
}

// renderNodeInput is the sliver of the render input this guest reads: the node's
// id, and whether the host handed it a workload to synthesize.
type renderNodeInput struct {
	Node struct {
		ID string `json:"id"`
	} `json:"node"`
	WorkloadProjection json.RawMessage `json:"workload_projection,omitempty"`
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
	var in renderNodeInput
	if err := json.Unmarshal(pdk.Input(), &in); err != nil {
		pdk.SetError(err)
		return 1
	}

	// A synthesis: one ordinary file, and one render fact whose value is the
	// kind the node's id names.
	if len(in.WorkloadProjection) > 0 {
		value, named := factValues[in.Node.ID]
		if !named {
			value = conformingFactValue
		}
		return output(renderNodeOutput{
			Files:       []renderedFile{{Path: synthesizedPath, Content: []byte("kind: Workload\n")}},
			RenderFacts: []renderFact{{Name: factName, Value: json.RawMessage(value)}},
		})
	}

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
