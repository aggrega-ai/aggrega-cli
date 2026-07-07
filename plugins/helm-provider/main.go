// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Command helm-provider is the helm Provider Wasm guest. It implements the
// sourced-render path of the helm engine behind the provider render ABI's
// render_node export, reusing the same filesystem-free render core the in-tree
// helm engine runs so the two produce byte-identical output for identical input.
//
// The guest decodes a RenderNodeInput, resolves the chart root from the payload's
// name-only SourceListing, asks the host to pack the chart's file tree through the
// pack-source host function (the guest touches no filesystem itself),
// reconstructs the Helm chart in memory, renders it against the node's values
// through the shared render core, and returns each rendered manifest's serialized
// bytes as a RenderedFile.
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
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helm"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
	pdk "github.com/extism/go-pdk"
	"helm.sh/helm/v3/pkg/chart/loader"
)

// pluginDescriptor is the static describe() self-report: a v1alpha1
// provider plugin declaring the render_node export, compatibility with
// the helm provider engine, and its plugin-level capability self-report
// (the input kinds it renders and how it treats a dispatch group's output).
// render_node declares two host functions it imports: aggrega_pack_source, which
// packs the chart source the guest resolves from the host's name-only listing,
// and aggrega_fetch_external_input, which resolves a chart's remote values-schema
// references through the host's pinned external-input channel. The host
// cross-checks these declarations against the functions the guest actually
// imports. The host reads this once at load and validates it before invoking
// render_node.
const pluginDescriptor = `{"abi_version":"v1alpha1","plugin_type":"provider","compatible_providers":["helm"],"exports":[{"name":"render_node","input_pattern":"b","host_functions":["aggrega_fetch_external_input","aggrega_pack_source"],"stability":"experimental","description":"renders a Helm chart source into flat Kubernetes manifests, or synthesizes a workload into flat Kubernetes manifests"}],"capabilities":{"input_kinds":{"sourced":true,"synthesized":true,"artifacts_standalone":true},"boundary_assembly":"none","reusable_instance":true},"metadata":{"author":"aggrega","license":"Apache-2.0"}}`

//go:wasmexport describe
func describe() int32 {
	pdk.Output([]byte(pluginDescriptor))
	return 0
}

//go:wasmexport render_node
func renderNode() int32 {
	var in rendercore.RenderNodeInput
	if err := in.UnmarshalJSON(pdk.Input()); err != nil {
		pdk.SetError(err)
		return 1
	}

	var out rendercore.RenderNodeOutput
	var err error
	switch {
	case in.WorkloadProjection != nil:
		out, err = renderSynthFromInput(in)
	case in.SourceListing != nil:
		out, err = renderSourcedFromInput(in)
	case in.ArtifactsProjection != nil:
		out, err = placeArtifactsFromInput(in)
	default:
		pdk.SetError(errors.New("helm-provider: render_node input carries neither a workload projection, a source listing, nor an artifacts projection"))
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
func nodeCtxFromInput(in rendercore.RenderNodeInput) rendercore.NodeRenderContext {
	return rendercore.NodeRenderContext{
		NodeID:           in.Node.ID,
		Annotations:      mergeAnnotations(in.Context.Annotations),
		BoundaryPath:     in.Node.BoundaryPath,
		SyncWave:         in.Node.SyncWave,
		RecipeName:       in.Node.RecipeName,
		RecipeVersion:    in.Node.RecipeVersion,
		LabelComponentID: in.Node.LabelComponentID,
		ProviderName:     in.Node.Provenance.ProviderName,
		ProviderVersion:  in.Node.Provenance.ProviderVersion,
		Provenance: rendercore.ProvenanceContext{
			CompilerVersion: in.Node.Provenance.CompilerVersion,
			AggregateName:   in.Node.Provenance.AggregateName,
			Environment:     in.Node.Provenance.Environment,
		},
		YAMLIndent: in.Context.YAMLIndent,
	}
}

// renderSourcedFromInput resolves the Helm chart root from the wire input's
// name-only source listing, asks the host to pack the chart's file tree,
// reconstructs the chart in memory, runs the same filesystem-free render core the
// in-tree engine runs, and returns each rendered manifest's serialized bytes as
// a RenderedFile. The node's values, identity, provenance, and merged
// annotations arrive in the node projection and render context.
//
// Values-schema validation runs hermetically: the chart's values.schema.json
// (and dependency charts' schemas) are validated by the render core's
// hermetic validator, whose chart-local references resolve from the packed
// chart files and whose remote http/https references resolve through the
// host's pinned external-input channel. The SDK's own live-fetching validator
// never runs in the guest. Validation only gates — it never alters values —
// so rendered bytes stay byte-identical with the in-tree engine.
func renderSourcedFromInput(in rendercore.RenderNodeInput) (rendercore.RenderNodeOutput, error) {
	if in.SourceListing == nil {
		return rendercore.RenderNodeOutput{}, errors.New("helm-provider: render_node input carries no source listing")
	}

	proj, err := packChartSource(in.SourceListing)
	if err != nil {
		return rendercore.RenderNodeOutput{}, err
	}

	bufs := make([]*loader.BufferedFile, 0, len(proj.Files))
	for _, f := range proj.Files {
		bufs = append(bufs, &loader.BufferedFile{Name: f.Path, Data: f.Content})
	}
	ch, err := loader.LoadFiles(bufs)
	if err != nil {
		return rendercore.RenderNodeOutput{}, err
	}

	values := rendercore.OrderedMapToPlain(&in.Node.Values)
	nodeCtx := nodeCtxFromInput(in)

	rendered, err := helm.RenderLoadedChart(context.Background(), ch, values, nodeCtx,
		helm.NewHermeticValuesValidator(aggrega.FetchExternalInput))
	if err != nil {
		return rendercore.RenderNodeOutput{}, err
	}

	out := rendercore.RenderNodeOutput{Files: make([]rendercore.RenderedFile, 0, len(rendered))}
	for _, path := range sortedKeys(rendered) {
		out.Files = append(out.Files, rendercore.RenderedFile{Path: path, Content: rendered[path]})
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
func renderSynthFromInput(in rendercore.RenderNodeInput) (rendercore.RenderNodeOutput, error) {
	if in.WorkloadProjection == nil {
		return rendercore.RenderNodeOutput{}, errors.New("helm-provider: render_node input carries no workload projection")
	}

	nodeCtx := nodeCtxFromInput(in)

	emitted, err := helm.SynthesizeWorkloadCore(&in.WorkloadProjection.Workload, in.WorkloadProjection.Secrets, nodeCtx)
	if err != nil {
		return rendercore.RenderNodeOutput{}, err
	}

	out := rendercore.RenderNodeOutput{Files: make([]rendercore.RenderedFile, 0, len(emitted))}
	for _, f := range emitted {
		out.Files = append(out.Files, rendercore.RenderedFile{Path: f.Name, Content: f.Body})
	}
	return out, nil
}

// placeArtifactsFromInput places the node's pre-evaluated artifact set into flat
// Kubernetes manifests. helm emits each artifact at its author-resolved name and
// splices the host-computed build provenance into the manifest's metadata via the
// shared placer, using the default "{{"/"}}" delimiters (helm's artifact bodies
// carry no surviving downstream template syntax, so a template-output parse
// failure is a hard authoring error). Entries are placed in author order.
func placeArtifactsFromInput(in rendercore.RenderNodeInput) (rendercore.RenderNodeOutput, error) {
	if in.ArtifactsProjection == nil {
		return rendercore.RenderNodeOutput{}, errors.New("helm-provider: render_node input carries no artifacts projection")
	}
	indent := in.Context.YAMLIndent
	var out rendercore.RenderNodeOutput
	for _, ent := range in.ArtifactsProjection.Entries {
		body, warning, err := rendercore.SpliceArtifactProvenance(ent.Name, ent.Content, ent.Labels, ent.Annotations, true, indent)
		if err != nil {
			return rendercore.RenderNodeOutput{}, err
		}
		if warning != "" {
			out.Diagnostics = append(out.Diagnostics, renderctx.Diagnostic{Severity: "warning", Code: "artifact_provenance_skipped", Message: warning})
		}
		out.Files = append(out.Files, rendercore.RenderedFile{Path: ent.Name, Content: body})
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

// packChartSource resolves the chart root from the host's name-only listing and
// asks the host to pack the chart's file tree rooted there. Chart-root resolution
// is single-sourced in the helm package (shared by the guest here), so the guest
// only wires the resolved subtree into a pack request. The chart root is packed
// as a tree, so the packed projection carries every chart file keyed by its
// chart-root-relative path — the same file set the host formerly packed.
func packChartSource(listing *rendercore.SourceListing) (*aggrega.SourceProjection, error) {
	rel, err := helm.ResolveChartRootRel(listing)
	if err != nil {
		return nil, fmt.Errorf("helm: locate chart root in %q: %w", listing.Root, err)
	}
	return aggrega.PackSource("tree", rel)
}

func main() {}
