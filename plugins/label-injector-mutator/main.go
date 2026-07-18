// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Command label-injector-mutator is an embedded mutator Wasm guest. It reads a
// rule set — a list of match/set pairs — from its attachment settings or from a
// definition file, and for every topology node whose existing labels contain all
// of a rule's match labels it emits JSON Patch documents that add the rule's set
// labels. This expresses org-wide tagging and injecting a selector label so a
// shared Feature attaches to the matching components. It self-describes as a
// mutator plugin whose mutate export the host validates before dispatch; the
// rule evaluation and patch construction live in mutatorcore, which is exercised
// directly on the host toolchain, so this guest holds only the ABI decode, the
// call, and the ABI encode.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o label-injector.wasm ./plugins/label-injector-mutator
package main

import (
	pdk "github.com/extism/go-pdk"

	"github.com/aggrega-ai/aggrega-cli/plugins/mutatorcore"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
)

// descriptor is the describe() self-report: a v1alpha1 mutator plugin whose
// mutate export the host validates before dispatch. A mutator is attached by URI
// at the mutator socket rather than matched by a served capability, so it
// advertises none.
func descriptor() aggrega.PluginDescriptor {
	return aggrega.PluginDescriptor{
		AbiVersion:  aggrega.APIVersion,
		PluginType:  "mutator",
		Description: "Add labels to topology nodes whose existing labels match a rule set.",
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

//go:wasmexport mutate
func mutate() int32 {
	var in aggrega.MutatorInput
	if err := in.UnmarshalJSON(pdk.Input()); err != nil {
		pdk.SetError(err)
		return 1
	}
	patches, err := mutatorcore.LabelInjectorPatches(in.Graph.Nodes, in.Settings, in.Sources)
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	out, err := aggrega.MutatorOutput{Patches: patches}.MarshalJSON()
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	pdk.Output(out)
	return 0
}

func main() {}
