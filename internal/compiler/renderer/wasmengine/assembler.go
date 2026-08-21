// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasmengine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
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
// It builds its embedded render Engine via New, so plugin identity and the
// describe()-sourced capabilities stay single-sourced across the two adapters.
// The loader is shared across the build so its compilation cache (when present)
// warms once; a nil loader is an injection-site bug rather than a supported zero
// value, matching New's contract.
func NewAssembler(lookup pluginpath.Lookup, pluginURI string, loader *wasm.Loader) *AssemblerEngine {
	return &AssemblerEngine{Engine: New(lookup, pluginURI, loader)}
}

// AssembleBoundary projects the host BoundaryInfo onto the assemble_boundary wire
// input, drives the guest export, and folds the guest's per-file output back into
// the BoundaryAssembler's path→AssembledFile map — carrying each file's
// producer-declared format alongside its bytes — plus the guest's nest-per-node
// signal.
// Rendered nodes are emitted in sorted-by-id order and each node's annotations are
// inserted in sorted-key order, so the wire payload is byte-identical across runs
// of identical input (the determinism contract). The boundary metadata is left
// unset: a dispatch group can merge contributors from multiple boundaries, so the
// per-render boundary is carry-only and not synthesized here. Each node's
// deployment wave travels with the node for that same reason — a merged group has
// no single boundary to read one wave off, and the multi-boundary case is exactly
// where one assembled artifact holds more than one wave. A duplicate output path
// from the guest is a loud error rather than a silent last-writer-wins overwrite.
//
// When the provider projects sub-charts (an umbrella assembler), the adapter
// populates the wire's sub-chart carriers and umbrella scalars from the holder's
// host-side projection — including the pristine bytes of each vendored
// Chart.yaml the engine reads at compile time — so the guest assembles the
// umbrella from pure data alone. Each sub-chart's values are inserted in sorted
// key order for the determinism contract. A provider that does not project
// sub-charts contributes none of these carriers.
func (e *AssemblerEngine) AssembleBoundary(ctx context.Context, info renderer.BoundaryInfo) (map[string]renderer.AssembledFile, bool, error) {
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
			SyncWave:    info.NodeSyncWave[id],
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

	// Populate the umbrella scalars only when the provider guest assembles an
	// umbrella (boundary-assembly "nest"). Asking the guest's own self-report
	// "do you nest per node?" — not "are you helm-chart?" — keeps the adapter
	// provider-agnostic and keeps a flat-assembling provider's wire free of these
	// carriers. The host performs the pristine Chart.yaml read here (the guest
	// has no filesystem) via the generic projectSubCharts core step so the guest
	// assembles from pure data alone; the adapter never touches the filesystem.
	if e.BoundaryAssembly() == wasm.BoundaryAssemblyNest {
		projs, err := projectSubCharts(info.SubCharts)
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
	if err := surfaceAssembleDiagnostics(info.BoundaryPath, out.Diagnostics, info.WarningSink); err != nil {
		return nil, false, err
	}

	files := make(map[string]renderer.AssembledFile, len(out.Files))
	for _, f := range out.Files {
		if _, dup := files[f.Path]; dup {
			return nil, false, fmt.Errorf("wasmengine: assembler guest emitted duplicate output path %q", f.Path)
		}
		files[f.Path] = renderer.AssembledFile{Content: f.Content, Format: f.Format}
	}
	return files, out.NestPerNode, nil
}

// invokeAssemble acquires a guest instance through the shared loader, calls the
// named assemble export against the embedded Engine's cached descriptor, and decodes
// the AssembleBoundaryOutput. It mirrors Engine.invoke's memoized-descriptor
// instance lifecycle for the assemble surface — the two share the loader, lookup,
// descriptor memo, and reused instance (when the guest declares reusable_instance)
// but not the codec, since the assemble wire shapes differ from the render surface.
// The descriptor read here is the embedded Engine's memo (describe): describe() and
// its validation run once per provider, not once per crossing. When the guest makes
// no reuse claim the acquired instance is fresh per crossing and the returned
// release closes it, so dispatch groups stay isolated by construction.
func (e *AssemblerEngine) invokeAssemble(ctx context.Context, export string, input renderer.AssembleBoundaryInput) (renderer.AssembleBoundaryOutput, error) {
	desc, err := e.describe()
	if err != nil {
		return renderer.AssembleBoundaryOutput{}, err
	}

	plugin, release, err := e.acquireInstance(ctx)
	if err != nil {
		return renderer.AssembleBoundaryOutput{}, err
	}
	defer release()

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
	if err := wasm.GuestReturnedError(export, out.Error); err != nil {
		return renderer.AssembleBoundaryOutput{}, fmt.Errorf("wasmengine: %w", err)
	}
	if err := wasm.GuestDiagnosticSeverities(e.pluginURI, export, out.Diagnostics); err != nil {
		return renderer.AssembleBoundaryOutput{}, fmt.Errorf("wasmengine: %w", err)
	}
	// Fold guest-emitted warnings into the assemble output's diagnostics as
	// warn-severity entries, mirroring the render surface, so both guest
	// advisory channels reach the caller through one list. Drained before the
	// deferred Close.
	for _, w := range plugin.DrainWarnings() {
		out.Diagnostics = append(out.Diagnostics, renderer.Diagnostic{Severity: abi.SeverityWarn, Message: w})
	}
	return out, nil
}

// surfaceAssembleDiagnostics routes a boundary assembler's diagnostics: warning-
// and info-severity entries go to the dispatch group's warning sink (nil-safe,
// each prefixed with the boundary path), and halt-severity entries are joined
// into a returned error naming the boundary. Warnings never affect emitted bytes.
func surfaceAssembleDiagnostics(boundaryPath string, diags []renderer.Diagnostic, sink func(string)) error {
	errs, advisories := renderer.SplitDiagnostics(diags)
	for _, a := range advisories {
		if sink != nil {
			sink(fmt.Sprintf("boundary %q: %s", boundaryPath, a))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("wasmengine: boundary %q: assembler reported errors: %s",
			boundaryPath, strings.Join(errs, "; "))
	}
	return nil
}

// projectSubCharts reads each sourced sub-chart's pristine Chart.yaml from disk
// into a filesystem-free projection, carrying instance identity, version, and
// per-instance values through. It is the single home for the umbrella's pristine
// Chart.yaml read — the invariant-forced host floor an umbrella-assembling
// provider needs because the guest has no filesystem and the per-node rendered
// files are not on the assemble wire. It is provider-independent: it reads
// `<SubChartInfo.Source>/Chart.yaml` for any umbrella assembler, so the assembler
// projects each sub-chart through it and the guest assembles the umbrella from
// pure data alone. A generative sub-chart (nil/empty source) carries no ChartYAML
// and takes the generative fallback in the assembly core.
func projectSubCharts(subCharts []renderer.SubChartInfo) ([]renderer.SubChartProjection, error) {
	projs := make([]renderer.SubChartProjection, 0, len(subCharts))
	for _, sc := range subCharts {
		proj := renderer.SubChartProjection{
			InstanceID: sc.InstanceID,
			Version:    sc.Version,
			Values:     sc.Values,
		}
		if s, ok := sc.Source.(string); ok && s != "" {
			data, err := os.ReadFile(filepath.Join(s, "Chart.yaml"))
			if err != nil {
				return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: s}, fmt.Sprintf("umbrella sourced: read Chart.yaml in %q", s))
			}
			proj.ChartYAML = data
		}
		projs = append(projs, proj)
	}
	return projs, nil
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
