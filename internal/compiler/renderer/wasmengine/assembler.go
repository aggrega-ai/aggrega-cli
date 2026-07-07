// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasmengine

import (
	"context"
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// AssembleBoundaryExport is the guest export this adapter drives to merge a
// dispatch group's per-node render outputs into one assembled artifact. It is
// exported so the dispatch that routes a provider to this adapter can require the
// catalog Plugin entity to advertise the same export the adapter will drive — one
// name, one source of truth, no drift between the routing precondition and the
// call. It is a distinct guest entrypoint from the render export, so a provider
// whose guest omits it (a flat-output provider) is simply never routed here.
const AssembleBoundaryExport = "assemble_boundary"

// AssemblerEngine assembles a dispatch group's per-node render outputs into a
// single artifact through a provider Wasm guest, implementing the host
// renderer.BoundaryAssembler. It embeds the sibling Engine to reuse its plugin
// resolution and shared per-build loader; the assemble surface has its own wire
// shapes (AssembleBoundaryInput to AssembleBoundaryOutput), so it carries its own
// invoke helper rather than the render Engine's. The adapter is provider-agnostic:
// it drives whichever guest the provider advertises.
type AssemblerEngine struct {
	*Engine
}

var _ renderer.BoundaryAssembler = (*AssemblerEngine)(nil)

// NewAssembler returns an AssemblerEngine that assembles through the guest at
// pluginURI, resolving its bytes via lookup and loading through the shared loader.
// It carries the same host-side capability holder the render adapter carries —
// via New — so plugin identity and input-kind capabilities stay single-sourced
// across the two adapters. The loader is shared across the build so its
// compilation cache (when present) warms once; a nil loader is an injection-site
// bug rather than a supported zero value, matching New's contract.
func NewAssembler(lookup pluginpath.Lookup, pluginURI string, hostCaps renderer.HostCapabilities, loader *wasm.Loader) *AssemblerEngine {
	return &AssemblerEngine{Engine: New(lookup, pluginURI, hostCaps, loader)}
}

// AssembleBoundary projects the host BoundaryInfo onto the assemble_boundary wire
// input, drives the guest export, and folds the guest's per-file output back into
// the BoundaryAssembler's path→bytes map plus the guest's nest-per-node signal.
// Rendered nodes are emitted in sorted-by-id order and each node's annotations are
// inserted in sorted-key order, so the wire payload is byte-identical across runs
// of identical input (the determinism contract). The boundary metadata is left
// unset: a dispatch group can merge contributors from multiple boundaries, so the
// per-render boundary is carry-only and not synthesized here. A duplicate output
// path from the guest is a loud error rather than a silent last-writer-wins
// overwrite.
//
// When the provider projects sub-charts (an umbrella assembler), the adapter
// populates the wire's sub-chart carriers and umbrella scalars from the holder's
// host-side projection — including the pristine bytes of each vendored
// Chart.yaml the engine reads at compile time — so the guest assembles the
// umbrella from pure data alone. Each sub-chart's values are inserted in sorted
// key order for the determinism contract. A provider that does not project
// sub-charts contributes none of these carriers.
func (e *AssemblerEngine) AssembleBoundary(ctx context.Context, info renderer.BoundaryInfo) (map[string][]byte, bool, error) {
	nodeIDs := make([]string, 0, len(info.IntermediatesByNode))
	for id := range info.IntermediatesByNode {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Strings(nodeIDs)

	renderedNodes := make([]renderer.RenderedNode, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		fragments := info.IntermediatesByNode[id]
		files := make([]renderer.RenderedFile, 0, len(fragments))
		for _, f := range fragments {
			files = append(files, renderer.RenderedFile{Path: f.Name, Content: f.Body})
		}
		renderedNodes = append(renderedNodes, renderer.RenderedNode{
			ID:          id,
			Type:        info.NodeComponent[id],
			Rendered:    renderer.RenderNodeOutput{Files: files},
			Annotations: orderedAnnotations(info.NodeAnnotations[id]),
		})
	}

	edges := make([]renderer.RenderEdge, 0, len(info.Edges))
	for _, ed := range info.Edges {
		edges = append(edges, renderer.RenderEdge{Source: ed.Source, Target: ed.Target})
	}

	input := renderer.AssembleBoundaryInput{
		RenderedNodes: renderedNodes,
		Context:       renderer.RenderContext{YAMLIndent: info.YAMLIndent},
		BoundaryPath:  info.BoundaryPath,
		Edges:         edges,
	}

	// Populate the umbrella scalars only when the provider projects sub-charts
	// (an umbrella assembler). Asking the provider's host capabilities "do you
	// project sub-charts?" — not "are you helm-chart?" —
	// keeps the adapter provider-agnostic and keeps a flat-assembling provider's
	// wire free of these carriers. The host performs the pristine Chart.yaml read
	// here so the guest assembles from pure data alone; the adapter never touches
	// the filesystem.
	if e.hostCaps.ProjectSubCharts != nil {
		projs, err := e.hostCaps.ProjectSubCharts(info.SubCharts)
		if err != nil {
			return nil, false, err
		}
		subCharts := make([]renderer.SubChart, 0, len(projs))
		for _, p := range projs {
			subCharts = append(subCharts, renderer.SubChart{
				InstanceID: p.InstanceID,
				Version:    p.Version,
				Values:     valuesToOrdered(p.Values),
				ChartYaml:  p.ChartYAML,
			})
		}
		input.SubCharts = subCharts
		input.ChartName = info.ChartName
		input.AggregateName = info.AggregateName
	}

	out, err := e.invokeAssemble(ctx, AssembleBoundaryExport, input)
	if err != nil {
		return nil, false, err
	}

	files := make(map[string][]byte, len(out.Files))
	for _, f := range out.Files {
		if _, dup := files[f.Path]; dup {
			return nil, false, fmt.Errorf("wasmengine: assembler guest emitted duplicate output path %q", f.Path)
		}
		files[f.Path] = f.Content
	}
	return files, out.NestPerNode, nil
}

// invokeAssemble loads a fresh guest instance through the shared loader, calls the
// named assemble export against the embedded Engine's cached descriptor, and decodes
// the AssembleBoundaryOutput. It mirrors Engine.invoke's memoized-descriptor,
// fresh-instance-per-crossing lifecycle for the assemble surface — the two share the
// loader, lookup, and descriptor memo but not the codec, since the assemble wire
// shapes differ from the render surface. The descriptor read here is the embedded
// Engine's memo (describe): describe() and its validation run once per provider, not
// once per crossing. Each call loads and closes its own plugin instance, so dispatch
// groups stay isolated by construction. The plugin is closed before invokeAssemble
// returns.
func (e *AssemblerEngine) invokeAssemble(ctx context.Context, export string, input renderer.AssembleBoundaryInput) (renderer.AssembleBoundaryOutput, error) {
	desc, err := e.describe()
	if err != nil {
		return renderer.AssembleBoundaryOutput{}, err
	}

	resolved, err := e.lookup(e.pluginURI)
	if err != nil {
		return renderer.AssembleBoundaryOutput{}, fmt.Errorf("wasmengine: resolve plugin %q: %w", e.pluginURI, err)
	}

	plugin, err := e.loader.Load(ctx, resolver.PluginSpec(e.pluginURI, resolved))
	if err != nil {
		return renderer.AssembleBoundaryOutput{}, err
	}
	defer plugin.Close(ctx)

	payload, err := input.MarshalJSON()
	if err != nil {
		return renderer.AssembleBoundaryOutput{}, fmt.Errorf("wasmengine: marshal assemble_boundary input: %w", err)
	}
	raw, err := wasm.InvokeExport(ctx, plugin, *desc, export, payload)
	if err != nil {
		return renderer.AssembleBoundaryOutput{}, err
	}

	var out renderer.AssembleBoundaryOutput
	if err := out.UnmarshalJSON(raw); err != nil {
		return renderer.AssembleBoundaryOutput{}, fmt.Errorf("wasmengine: decode assemble_boundary output: %w", err)
	}
	return out, nil
}

// orderedAnnotations builds a deterministic ordered annotation map from a node's
// plain annotation map, inserting keys in sorted order so the wire payload is
// byte-identical across runs of identical input.
func orderedAnnotations(anns map[string]string) types.OrderedMap[string] {
	om := types.OrderedMap[string]{}
	for _, k := range sortedKeys(anns) {
		om.Set(k, anns[k])
	}
	return om
}
