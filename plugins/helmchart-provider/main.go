// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Command helmchart-provider is the helm-chart Provider Wasm guest. It implements
// the sourced-render path of the helm-chart engine behind the provider render
// ABI's render_node export, reusing the same filesystem-free render core the
// in-tree helm-chart engine runs so the two produce byte-identical output for
// identical input.
//
// The helm-chart provider is sourced-only: it copies a user's local Helm chart
// verbatim into a sub-chart file set (umbrella + sub-chart composition). It
// synthesizes nothing from a Workload — workload synthesis is the helm provider's
// concern — so the guest handles a source projection alone and rejects a payload
// that carries none.
//
// The guest decodes a RenderNodeInput, reconstructs the sub-chart's file set in
// memory from the payload's SourceProjection (the host read the chart's files at
// compile time and packed them into the payload — the guest touches no
// filesystem), and returns each file's bytes verbatim as a RenderedFile. The host
// writes those bytes verbatim and runs no serializer, so the on-disk output
// matches the in-tree engine's bytes.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o helmchart-provider.wasm ./plugins/helmchart-provider
// (c-shared produces a WASI reactor whose _initialize the host runs before
// the first export, so the Go runtime is initialized before render_node.)
package main

import (
	"errors"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helmchart"
	pdk "github.com/extism/go-pdk"
)

// pluginDescriptor is the static describe() self-report: a v1alpha1 provider
// plugin declaring the render_node export and compatibility with the helm-chart
// provider engine. The host reads this once at load and validates it before
// invoking render_node. helm-chart copies chart files verbatim and fetches
// nothing, so no host functions are declared.
const pluginDescriptor = `{"abi_version":"v1alpha1","plugin_type":"provider","compatible_providers":["helm-chart"],"exports":[{"name":"render_node","input_pattern":"b","stability":"experimental","description":"copies a sourced Helm chart into a sub-chart file set"}],"metadata":{"author":"aggrega","license":"Apache-2.0"}}`

//go:wasmexport describe
func describe() int32 {
	pdk.Output([]byte(pluginDescriptor))
	return 0
}

//go:wasmexport render_node
func renderNode() int32 {
	var in renderer.RenderNodeInput
	if err := in.UnmarshalJSON(pdk.Input()); err != nil {
		pdk.SetError(err)
		return 1
	}

	// helm-chart is sourced-only: it copies a local chart verbatim and
	// synthesizes nothing. A payload with no source projection is an orchestrator
	// dispatch error rather than authoring drift, so it halts loudly.
	if in.SourceProjection == nil {
		pdk.SetError(errors.New("helmchart-provider: render_node input carries no source projection"))
		return 1
	}

	rendered := helmchart.RenderSourcedChartCore(in.SourceProjection)

	out := renderer.RenderNodeOutput{Files: make([]renderer.RenderedFile, 0, len(rendered))}
	for _, path := range sortedKeys(rendered) {
		out.Files = append(out.Files, renderer.RenderedFile{Path: path, Content: rendered[path]})
	}

	encoded, err := out.MarshalJSON()
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	pdk.Output(encoded)
	return 0
}

// sortedKeys returns the map's keys in lexical order so file emission is
// deterministic regardless of Go map iteration order.
func sortedKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// insertion sort keeps the guest free of the sort package's
	// reflection surface; the file set per node is small.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

func main() {}
