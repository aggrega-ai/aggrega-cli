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

// RenderNodeExport is the guest export this adapter drives for both sourced
// render and workload synthesis; the helm provider guest routes on which
// projection the RenderNodeInput carries (source vs workload). It is exported so
// the dispatch that routes a provider to this adapter can require the catalog
// Plugin entity to advertise the same export the adapter will drive — one name,
// one source of truth, no drift between the routing precondition and the call.
const RenderNodeExport = "render_node"

// Engine renders one node through a provider Wasm guest, implementing the host
// renderer.Engine and renderer.WorkloadSynthesizer interfaces. It resolves the
// guest's bytes via lookup, loads them through the injected shared loader once
// per call, and reports the input-kind capabilities of the Go engine it wraps.
// The adapter is provider-agnostic: it drives whichever guest the wrapped Go
// engine advertises, and projects a sourced node's source through the wrapped
// engine's own SourceProjector rather than any hardcoded provider package.
type Engine struct {
	lookup    pluginpath.Lookup
	pluginURI string
	// goEngine is the in-tree Go engine this adapter fronts. Its Capabilities
	// gate the input kinds (so the render-time gate stays Go-decided), and its
	// SourceProjector projects a sourced node's source onto the render wire —
	// the seam that keeps the adapter free of any provider package import.
	goEngine renderer.Engine
	caps      renderer.ProviderCapabilities
	// loader is the shared per-build loader every Render / SynthesizeWorkload
	// call loads through. It carries the build's external-input service and,
	// when constructed via wasm.NewCachingLoader, a shared compilation cache so
	// the guest compiles once per build rather than once per node. Each call
	// still loads a fresh, isolated plugin instance through it.
	loader *wasm.Loader
}

var (
	_ renderer.Engine              = (*Engine)(nil)
	_ renderer.WorkloadSynthesizer = (*Engine)(nil)
)

// New returns an Engine that renders through the guest at pluginURI, resolving
// its bytes via lookup and loading through the shared loader. It wraps goEngine
// — the in-tree Go engine for the same provider — deriving its input-kind
// capabilities from goEngine.Capabilities() so the render-time gate stays
// Go-decided, and holding goEngine so a sourced Render projects the source
// through goEngine's SourceProjector. The loader is shared across the build so
// its compilation cache (when present) warms once; a nil loader is an
// injection-site bug — every call would nil-panic — rather than a supported
// zero value.
func New(lookup pluginpath.Lookup, pluginURI string, goEngine renderer.Engine, loader *wasm.Loader) *Engine {
	return &Engine{lookup: lookup, pluginURI: pluginURI, goEngine: goEngine, caps: goEngine.Capabilities(), loader: loader}
}

// Capabilities reports the input-kind capabilities configured at construction.
func (e *Engine) Capabilities() renderer.ProviderCapabilities {
	return e.caps
}

// Render projects the sourced node into a render_node payload and returns the
// guest's per-file output as a path→bytes map. nodeCtx.Source must be the
// resolved filesystem path the orchestrator populates before dispatch; a
// non-string value is an orchestrator bug rather than authoring drift, matching
// the in-tree engine's contract. A duplicate output path from the guest is a
// loud error rather than a silent last-writer-wins overwrite.
func (e *Engine) Render(ctx context.Context, values map[string]any, nodeCtx renderer.NodeRenderContext) (map[string][]byte, error) {
	src, ok := nodeCtx.Source.(string)
	if !ok {
		return nil, fmt.Errorf(
			"wasmengine: node %q: internal error — Render dispatched with non-string source %T; the orchestrator resolves spec.source to a filesystem path before dispatch",
			nodeCtx.NodeID, nodeCtx.Source)
	}

	projector, ok := e.goEngine.(renderer.SourceProjector)
	if !ok {
		return nil, fmt.Errorf(
			"wasmengine: node %q: internal error — provider engine %T was routed to a Wasm guest for sourced render but does not implement renderer.SourceProjector",
			nodeCtx.NodeID, e.goEngine)
	}

	proj, err := projector.ProjectSource(src)
	if err != nil {
		return nil, err
	}

	input := renderer.RenderNodeInput{
		Node:             e.nodeProjection(nodeCtx, valuesToOrdered(values)),
		Context:          e.renderContext(nodeCtx),
		SourceProjection: proj,
	}
	out, err := e.invoke(ctx, RenderNodeExport, input)
	if err != nil {
		return nil, err
	}

	files := make(map[string][]byte, len(out.Files))
	for _, f := range out.Files {
		if _, dup := files[f.Path]; dup {
			return nil, fmt.Errorf("wasmengine: node %q: guest emitted duplicate output path %q", nodeCtx.NodeID, f.Path)
		}
		files[f.Path] = f.Content
	}
	return files, nil
}

// SynthesizeWorkload projects the evaluated workload and its per-slot secret
// provides into a render_node payload and returns the guest's synthesized
// manifests in emission order. Emission order is deterministic and load-bearing,
// so the guest's file order is preserved — never re-sorted. The per-slot secret
// provides are projected from the template context through the same host
// projection the in-tree engine uses, so both surfaces resolve a workload secret
// reference to the same generated Secret identity.
//
// Each emitted file carries the same Intermediate marking the wrapped Go engine
// would set on its own synthesis output, classified through the wrapped engine
// (renderer.RenderIsIntermediate): a BoundaryAssembler provider (compose) marks
// its fragments intermediate so the orchestrator routes them to AssembleBoundary
// rather than writing them at the dispatch path, while a flat-output provider
// (helm) marks them false. Reproducing the flag here keeps a provider routed to
// Wasm byte-for-byte equivalent to the in-tree engine it stands in for.
func (e *Engine) SynthesizeWorkload(
	ctx context.Context,
	tmplCtx *types.TemplateContext,
	w *types.Workload,
	nodeCtx renderer.NodeRenderContext,
) ([]renderer.EmittedFile, error) {
	if w == nil {
		return nil, nil
	}

	input := renderer.RenderNodeInput{
		Node:    e.nodeProjection(nodeCtx, types.OrderedMap[any]{}),
		Context: e.renderContext(nodeCtx),
		WorkloadProjection: &renderer.WorkloadProjection{
			Workload: *w,
			Secrets:  renderer.SecretProvidesFromContext(tmplCtx),
		},
	}
	out, err := e.invoke(ctx, RenderNodeExport, input)
	if err != nil {
		return nil, err
	}

	intermediate := renderer.RenderIsIntermediate(e.goEngine)
	emitted := make([]renderer.EmittedFile, 0, len(out.Files))
	for _, f := range out.Files {
		emitted = append(emitted, renderer.EmittedFile{Name: f.Path, Body: f.Content, Intermediate: intermediate})
	}
	return emitted, nil
}

// invoke loads the guest through the shared describe-gated loader, calls the
// named export, and decodes the RenderNodeOutput. The shared loader carries the
// build's external-input service and its compilation cache, so a guest fetch
// resolves through the pinned channel (nil when none is attached — the guest
// then receives the in-band not-available envelope) and the guest's machine
// code compiles once per build. Each call still loads and closes its own plugin
// instance, so nodes stay isolated. The plugin is closed before invoke returns.
func (e *Engine) invoke(ctx context.Context, export string, input renderer.RenderNodeInput) (renderer.RenderNodeOutput, error) {
	resolved, err := e.lookup(e.pluginURI)
	if err != nil {
		return renderer.RenderNodeOutput{}, fmt.Errorf("wasmengine: resolve plugin %q: %w", e.pluginURI, err)
	}

	plugin, desc, err := e.loader.LoadAndDescribe(ctx, resolver.PluginSpec(e.pluginURI, resolved))
	if err != nil {
		return renderer.RenderNodeOutput{}, err
	}
	defer plugin.Close(ctx)

	payload, err := input.MarshalJSON()
	if err != nil {
		return renderer.RenderNodeOutput{}, fmt.Errorf("wasmengine: marshal render_node input: %w", err)
	}
	raw, err := wasm.InvokeExport(ctx, plugin, desc, export, payload)
	if err != nil {
		return renderer.RenderNodeOutput{}, err
	}

	var out renderer.RenderNodeOutput
	if err := out.UnmarshalJSON(raw); err != nil {
		return renderer.RenderNodeOutput{}, fmt.Errorf("wasmengine: decode render_node output: %w", err)
	}
	return out, nil
}

// nodeProjection builds the wire node projection from the host node context —
// the inverse of the guest's own context reconstruction. It carries the identity
// and provenance the render core stamps onto every emitted manifest; type,
// resolved recipe, features, and uses edges are not part of the render surface
// and are left unset.
func (e *Engine) nodeProjection(nodeCtx renderer.NodeRenderContext, values types.OrderedMap[any]) renderer.IRNodeProjection {
	return renderer.IRNodeProjection{
		ID:               nodeCtx.NodeID,
		Values:           values,
		BoundaryPath:     nodeCtx.BoundaryPath,
		SyncWave:         nodeCtx.SyncWave,
		RecipeName:       nodeCtx.RecipeName,
		RecipeVersion:    nodeCtx.RecipeVersion,
		LabelComponentID: nodeCtx.LabelComponentID,
		Provenance: renderer.NodeProvenance{
			CompilerVersion: nodeCtx.Provenance.CompilerVersion,
			AggregateName:   nodeCtx.Provenance.AggregateName,
			Environment:     nodeCtx.Provenance.Environment,
		},
	}
}

// renderContext builds the wire render context from the host node context: the
// render-wide indentation width and the node's merged annotations flattened into
// a single bucket, so the guest's own bucket-merge reproduces the same flat map
// the in-tree engine reads.
func (e *Engine) renderContext(nodeCtx renderer.NodeRenderContext) renderer.RenderContext {
	return renderer.RenderContext{
		Annotations: flattenAnnotations(nodeCtx.Annotations),
		YAMLIndent:  nodeCtx.YAMLIndent,
	}
}

// flattenAnnotations places the already-merged host annotation map into a single
// wire bucket. The guest merges its three buckets into one flat map, so one
// populated bucket reproduces the host's merged map exactly. Keys are inserted in
// sorted order so the wire payload stays deterministic across runs.
func flattenAnnotations(anns map[string]string) renderer.RenderAnnotations {
	bucket := types.OrderedMap[string]{}
	for _, k := range sortedKeys(anns) {
		bucket.Set(k, anns[k])
	}
	return renderer.RenderAnnotations{Aggregate: bucket}
}

// valuesToOrdered converts the plain values map the engine interface delivers
// into the wire's ordered value map. Value order is not load-bearing for render
// (the guest flattens the ordered map back to a plain map before templating), but
// keys are inserted in sorted order so the wire payload stays deterministic.
func valuesToOrdered(values map[string]any) types.OrderedMap[any] {
	om := types.OrderedMap[any]{}
	for _, k := range sortedKeys(values) {
		om.Set(k, values[k])
	}
	return om
}

// sortedKeys returns m's keys in lexical order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
