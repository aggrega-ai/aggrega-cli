// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasmengine

import (
	"context"
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// renderNodeExport is the guest export this adapter drives for both sourced
// render and workload synthesis; the helm provider guest routes on which
// projection the RenderNodeInput carries (source vs workload).
const renderNodeExport = "render_node"

// Engine renders one node through the helm provider Wasm guest, implementing the
// host renderer.Engine and renderer.WorkloadSynthesizer interfaces. It resolves
// the guest's bytes via lookup, loads them through the production describe-gated
// loader once per call, and reports the input-kind capabilities it was
// constructed with.
type Engine struct {
	lookup    pluginpath.Lookup
	pluginURI string
	caps      renderer.ProviderCapabilities
}

var (
	_ renderer.Engine              = (*Engine)(nil)
	_ renderer.WorkloadSynthesizer = (*Engine)(nil)
)

// New returns an Engine that renders through the guest at pluginURI, resolving
// its bytes via lookup and reporting caps as its input-kind capabilities.
func New(lookup pluginpath.Lookup, pluginURI string, caps renderer.ProviderCapabilities) *Engine {
	return &Engine{lookup: lookup, pluginURI: pluginURI, caps: caps}
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

	proj, err := helm.ProjectChartSource(src)
	if err != nil {
		return nil, err
	}

	input := renderer.RenderNodeInput{
		Node:             e.nodeProjection(nodeCtx, valuesToOrdered(values)),
		Context:          e.renderContext(nodeCtx),
		SourceProjection: proj,
	}
	out, err := e.invoke(ctx, renderNodeExport, input)
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
			Secrets:  helm.SecretProvidesFromContext(tmplCtx),
		},
	}
	out, err := e.invoke(ctx, renderNodeExport, input)
	if err != nil {
		return nil, err
	}

	emitted := make([]renderer.EmittedFile, 0, len(out.Files))
	for _, f := range out.Files {
		emitted = append(emitted, renderer.EmittedFile{Name: f.Path, Body: f.Content})
	}
	return emitted, nil
}

// invoke loads the guest through the production describe-gated loader, calls the
// named export, and decodes the RenderNodeOutput. The loader carries the
// context's external-input service so a guest fetch resolves through the pinned
// channel (nil when none is attached — the guest then receives the in-band
// not-available envelope). The plugin is closed before invoke returns.
func (e *Engine) invoke(ctx context.Context, export string, input renderer.RenderNodeInput) (renderer.RenderNodeOutput, error) {
	resolved, err := e.lookup(e.pluginURI)
	if err != nil {
		return renderer.RenderNodeOutput{}, fmt.Errorf("wasmengine: resolve plugin %q: %w", e.pluginURI, err)
	}

	loader := &wasm.Loader{ExternalInputs: wasm.ExternalInputsFromContext(ctx)}
	plugin, desc, err := loader.LoadAndDescribe(ctx, resolver.PluginSpec(e.pluginURI, resolved))
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
