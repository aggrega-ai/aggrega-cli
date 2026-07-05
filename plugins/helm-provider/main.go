// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Command helm-provider is the helm Provider Wasm guest. It implements the
// sourced-render path of the helm engine behind the provider render ABI's
// render_node export, reusing the same filesystem-free render core the in-tree
// helm engine runs so the two produce byte-identical output for identical input.
//
// The guest decodes a RenderNodeInput, reconstructs the Helm chart in memory
// from the payload's SourceProjection (the host read the chart's files at
// compile time and packed them into the payload — the guest touches no
// filesystem), renders it against the node's values through the shared render
// core, and returns each rendered manifest's serialized bytes as a RenderedFile.
// The host writes those bytes verbatim and runs no serializer, so the on-disk
// output matches the in-tree engine's bytes.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o helm-provider.wasm ./plugins/helm-provider
// (c-shared produces a WASI reactor whose _initialize the host runs before
// the first export, so the Go runtime is initialized before render_node.)
package main

import (
	"context"
	"errors"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helm"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
	pdk "github.com/extism/go-pdk"
	"helm.sh/helm/v3/pkg/chart/loader"
)

// pluginDescriptor is the static describe() self-report: a v1alpha1
// provider plugin declaring the render_node export and compatibility with
// the helm provider engine. The host reads this once at load and validates
// it before invoking render_node.
const pluginDescriptor = `{"abi_version":"v1alpha1","plugin_type":"provider","compatible_providers":["helm"],"exports":[{"name":"render_node","input_pattern":"b","stability":"experimental","description":"renders a Helm chart source into flat Kubernetes manifests, or synthesizes a workload into flat Kubernetes manifests"}],"metadata":{"author":"aggrega","license":"Apache-2.0"}}`

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

	var out renderer.RenderNodeOutput
	var err error
	switch {
	case in.WorkloadProjection != nil:
		out, err = renderSynthFromInput(in)
	case in.SourceProjection != nil:
		out, err = renderSourcedFromInput(in)
	default:
		pdk.SetError(errors.New("helm-provider: render_node input carries neither a workload projection nor a source projection"))
		return 1
	}
	if err != nil {
		pdk.SetError(errors.New(errorChainMessage(err)))
		return 1
	}

	encoded, err := out.MarshalJSON()
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	pdk.Output(encoded)
	return 0
}

// errorChainMessage serializes err's full wrap chain into one message. The
// host-side rich error formatter is not available across the plugin ABI, and
// some wrapper types (the compiler's categorised errors) keep their cause out
// of Error(), surfacing it only through Unwrap; appending each unwrapped
// cause not already present keeps the root diagnostic — a violated values
// constraint, an unresolvable schema URL — in the message the host receives.
func errorChainMessage(err error) string {
	msg := err.Error()
	for e := errors.Unwrap(err); e != nil; e = errors.Unwrap(e) {
		if s := e.Error(); !strings.Contains(msg, s) {
			msg += ": " + s
		}
	}
	return msg
}

// nodeCtxFromInput builds the full render context for a node from the wire
// input: the node's identity and the provenance the render core stamps onto
// every emitted manifest (boundary path, deployment wave, Recipe identity,
// component-id override, and build identity) from the node projection, the
// merged annotations from the render context, and the render-wide YAML
// indentation width the guest serializes output at (carried on the render
// context; the render core clamps its zero value to the default width).
// Carrying the full provenance here is what makes the guest's rendered
// manifests carry the same aggrega.ai labels and annotations the in-tree engine
// emits, so the two produce byte-identical output for identical input.
func nodeCtxFromInput(in renderer.RenderNodeInput) renderer.NodeRenderContext {
	return renderer.NodeRenderContext{
		NodeID:           in.Node.ID,
		Annotations:      mergeAnnotations(in.Context.Annotations),
		BoundaryPath:     in.Node.BoundaryPath,
		SyncWave:         in.Node.SyncWave,
		RecipeName:       in.Node.RecipeName,
		RecipeVersion:    in.Node.RecipeVersion,
		LabelComponentID: in.Node.LabelComponentID,
		ProviderName:     in.Node.Provenance.ProviderName,
		ProviderVersion:  in.Node.Provenance.ProviderVersion,
		Provenance: renderer.ProvenanceContext{
			CompilerVersion: in.Node.Provenance.CompilerVersion,
			AggregateName:   in.Node.Provenance.AggregateName,
			Environment:     in.Node.Provenance.Environment,
		},
		YAMLIndent: in.Context.YAMLIndent,
	}
}

// renderSourcedFromInput reconstructs the Helm chart in memory from the wire
// input's source projection, runs the same filesystem-free render core the
// in-tree engine runs, and returns each rendered manifest's serialized bytes as
// a RenderedFile. The chart's files arrive in the source projection (the host
// read them at compile time); the node's values, identity, provenance, and
// merged annotations arrive in the node projection and render context.
//
// Values-schema validation runs hermetically: the chart's values.schema.json
// (and dependency charts' schemas) are validated by the render core's
// hermetic validator, whose chart-local references resolve from the projected
// chart files and whose remote http/https references resolve through the
// host's pinned external-input channel. The SDK's own live-fetching validator
// never runs in the guest. Validation only gates — it never alters values —
// so rendered bytes stay byte-identical with the in-tree engine.
func renderSourcedFromInput(in renderer.RenderNodeInput) (renderer.RenderNodeOutput, error) {
	if in.SourceProjection == nil {
		return renderer.RenderNodeOutput{}, errors.New("helm-provider: render_node input carries no source projection")
	}

	bufs := make([]*loader.BufferedFile, 0, len(in.SourceProjection.Files))
	for _, f := range in.SourceProjection.Files {
		bufs = append(bufs, &loader.BufferedFile{Name: f.Path, Data: f.Content})
	}
	ch, err := loader.LoadFiles(bufs)
	if err != nil {
		return renderer.RenderNodeOutput{}, err
	}

	values := renderer.OrderedMapToPlain(&in.Node.Values)
	nodeCtx := nodeCtxFromInput(in)

	rendered, err := helm.RenderLoadedChart(context.Background(), ch, values, nodeCtx,
		helm.NewHermeticValuesValidator(aggrega.FetchExternalInput))
	if err != nil {
		return renderer.RenderNodeOutput{}, err
	}

	out := renderer.RenderNodeOutput{Files: make([]renderer.RenderedFile, 0, len(rendered))}
	for _, path := range sortedKeys(rendered) {
		out.Files = append(out.Files, renderer.RenderedFile{Path: path, Content: rendered[path]})
	}
	return out, nil
}

// renderSynthFromInput synthesizes provider-native manifests from the wire
// input's workload projection, running the same filesystem-free synthesis core
// the in-tree engine runs, and returns each synthesized manifest's serialized
// bytes as a RenderedFile. The workload and its per-slot secret provides arrive
// in the workload projection; the node's identity, provenance, and merged
// annotations arrive in the node projection and render context. The core's
// emission order is deterministic and load-bearing, so the files are collected
// in order and never re-sorted.
func renderSynthFromInput(in renderer.RenderNodeInput) (renderer.RenderNodeOutput, error) {
	if in.WorkloadProjection == nil {
		return renderer.RenderNodeOutput{}, errors.New("helm-provider: render_node input carries no workload projection")
	}

	nodeCtx := nodeCtxFromInput(in)

	emitted, err := helm.SynthesizeWorkloadCore(&in.WorkloadProjection.Workload, in.WorkloadProjection.Secrets, nodeCtx)
	if err != nil {
		return renderer.RenderNodeOutput{}, err
	}

	out := renderer.RenderNodeOutput{Files: make([]renderer.RenderedFile, 0, len(emitted))}
	for _, f := range emitted {
		out.Files = append(out.Files, renderer.RenderedFile{Path: f.Name, Content: f.Body})
	}
	return out, nil
}

// mergeAnnotations flattens the three render-context annotation buckets
// into the single merged map the helm engine reads (for the namespace
// annotation). Later buckets win on key collision, matching the
// aggregate-then-release-then-catalog precedence.
func mergeAnnotations(a renderctx.RenderAnnotations) map[string]string {
	out := map[string]string{}
	for _, bucket := range []*types.OrderedMap[string]{&a.Aggregate, &a.Release, &a.Catalog} {
		for _, k := range bucket.Keys() {
			if v, ok := bucket.Get(k); ok {
				out[k] = v
			}
		}
	}
	return out
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
