// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build wasip1

// Command selector-tagger-mutator is an embedded governance Wasm guest for the
// label-transform role. It reads a component-to-tag map from a definition file
// and, for every topology node, sets a tag label to the value the map assigns
// the node's component, falling back to a default tag from its attachment
// settings when the component is absent from the map. It self-describes as a
// governance plugin whose mutate export the host validates before dispatch at a
// label-transform role; the mapping lookup and patch construction live in
// mutatorcore, which is exercised directly on the host toolchain, so this guest
// holds only the ABI decode, the call, and the ABI encode.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o selector-tagger.wasm ./plugins/selector-tagger-mutator
package main

import (
	pdk "github.com/extism/go-pdk"

	"github.com/aggrega-ai/aggrega-cli/plugins/mutatorcore"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// descriptor is the describe() self-report: a governance plugin whose
// mutate export the host validates before dispatch at a label-transform role. A
// governance guest is attached by URI at the governance socket rather than
// matched by a served capability, so it advertises none.
func descriptor() aggrega.PluginDescriptor {
	return aggrega.PluginDescriptor{
		AbiVersion:  aggrega.APIVersion,
		PluginType:  "governance",
		Description: "Set a tag label on each topology node from a component-to-tag map.",
		Exports:     []aggrega.PluginExport{{Name: "mutate"}},
		Metadata:    &aggrega.PluginDescriptorMeta{Author: "aggrega", License: "Apache-2.0"},
	}
}

//go:wasmexport describe
func describe() int32 {
	out, err := aggrega.MarshalDescriptor(descriptor())
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	pdk.Output(out)
	return 0
}

// mutate reads the label-transform payload, asks mutatorcore which labels the
// nodes should carry, and reports them as patch documents. The payload and the
// result both travel as canonical CBOR: this socket carries the binary encoding
// in both directions. The patch documents themselves stay JSON, because the
// socket carries them opaquely and neither side reads into them.
//
//go:wasmexport mutate
func mutate() int32 {
	var in aggrega.MutatorInput
	if err := in.DecodeCBOR(pdk.Input()); err != nil {
		pdk.SetError(err)
		return 1
	}
	patches, err := mutatorcore.SelectorTaggerPatches(in.Graph.Nodes, in.Settings, in.Sources)
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	out, err := aggrega.MutatorOutput{Patches: patches}.EncodeCBOR()
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	pdk.Output(out)
	return 0
}

func main() {}
