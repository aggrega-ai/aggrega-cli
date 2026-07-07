// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasmengine

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

// ValidateNodeInputExport is the optional guest export this adapter drives to
// structurally validate a node's resolved input before render dispatch. It is
// deliberately distinct from the whole-graph policy-validator socket's validate
// export: this one is per-node, provider-scoped, and consumes the same
// RenderNodeInput projection render_node receives. A provider whose guest does
// not advertise it in describe() is never invoked here, so the input-validation
// gate is a no-op for it.
const ValidateNodeInputExport = "validate_node_input"

// BoundaryEnvOverridesExport is the optional guest export this adapter drives to
// compute a boundary's per-consumer environment overrides — the boundary-to-
// consumer env channel a provider whose guest bridges endpoints across a network
// seam (compose) advertises. A guest whose describe() omits it is never invoked
// here, so the channel is a no-op for it and the host applies no overrides.
const BoundaryEnvOverridesExport = "boundary_env_overrides"

// Engine renders one node through a provider Wasm guest, implementing the host
// renderer.Engine and renderer.WorkloadSynthesizer interfaces. It resolves the
// guest's bytes via lookup and loads them through the injected shared loader once
// per call. The adapter is provider-agnostic: it drives whichever guest the
// provider advertises, and hands the guest a generic name-only enumeration of a
// sourced node's acquired source rather than packing bytes through any hardcoded
// provider package — the guest resolves and requests the bytes it needs through
// the pack-source host function.
type Engine struct {
	lookup    pluginpath.Lookup
	pluginURI string
	// loader is the shared per-build loader every Render / SynthesizeWorkload
	// call loads through. It carries the build's external-input service and,
	// when constructed via wasm.NewCachingLoader, a shared compilation cache so
	// the guest compiles once per build rather than once per node. Each call
	// still loads a fresh, isolated plugin instance through it.
	loader *wasm.Loader
	// descriptor caches the guest's validated describe() self-report, loaded once
	// per Engine. The input-kind gate (Capabilities), the output classification
	// (BoundaryAssembly), and every per-node crossing (invoke, invokeAssemble) read
	// the guest's plugin-level capability claim from it: the describe() call, the
	// descriptor validation, and the load-gate cross-checks (capability and
	// host-functions) are a pure function of the guest bytes — identical for every
	// node of the same provider — so they run once per Engine through this memo, not
	// once per node. The descriptor is a pure function of the guest bytes, so caching
	// it cannot alter rendered output. RenderAll drives a single sequential loop, so a
	// plain field needs no guard. descriptorErr caches a load/describe failure so a
	// broken guest is reported consistently rather than re-probed per call.
	descriptor    *wasm.PluginDescriptor
	descriptorErr error
}

var (
	_ renderer.Engine                   = (*Engine)(nil)
	_ renderer.NodeInputValidator       = (*Engine)(nil)
	_ renderer.WorkloadSynthesizer      = (*Engine)(nil)
	_ renderer.ArtifactsEmitter         = (*Engine)(nil)
	_ renderer.BoundaryEnvOverrider     = (*Engine)(nil)
	_ renderer.BoundaryAssemblyReporter = (*Engine)(nil)
	_ renderer.CodecProvider            = (*Engine)(nil)
)

// New returns an Engine that renders through the guest at pluginURI, resolving
// its bytes via lookup and loading through the shared loader. Its input kinds,
// boundary-assembly mode, codec, and optional export surface all come from the
// guest's live describe() descriptor rather than any host-side per-provider
// holder. The loader is shared across the build so its compilation cache (when
// present) warms once; a nil loader is an injection-site bug — every call would
// nil-panic — rather than a supported zero value.
func New(lookup pluginpath.Lookup, pluginURI string, loader *wasm.Loader) *Engine {
	return &Engine{lookup: lookup, pluginURI: pluginURI, loader: loader}
}

// Capabilities reports the input kinds the provider guest renders, read from its
// live describe() self-report (capabilities.input_kinds). The descriptor is loaded
// and cached on first use. When the guest cannot be loaded or described, this
// reports no supported input kind, so the central input-kind gate halts the node
// loudly rather than dispatching to an unloadable guest; the underlying load
// failure surfaces in full on the render path.
func (e *Engine) Capabilities() renderer.ProviderCapabilities {
	desc, err := e.describe()
	if err != nil {
		return renderer.ProviderCapabilities{}
	}
	ik := desc.Capabilities.InputKinds
	return renderer.ProviderCapabilities{
		Sourced:             ik.Sourced,
		Synthesized:         ik.Synthesized,
		ArtifactsStandalone: ik.ArtifactsStandalone,
	}
}

// BoundaryAssembly reports how the provider guest assembles a dispatch group's
// per-node output — "merge", "nest", or "none" — read from its live describe()
// self-report (capabilities.boundary_assembly). The descriptor is loaded and cached
// on first use; an empty string is returned when the guest cannot be loaded or
// described. It satisfies renderer.BoundaryAssemblyReporter, so the host's render
// classification reads the guest's own claim.
func (e *Engine) BoundaryAssembly() string {
	desc, err := e.describe()
	if err != nil {
		return ""
	}
	return desc.Capabilities.BoundaryAssembly
}

// describe loads the provider guest, reads and validates its describe()
// self-report, and caches the result (or the failure) on the Engine so the
// input-kind gate and the render classification read the guest's plugin-level
// capability claim without reloading. The descriptor is a pure function of the
// guest bytes, so caching it cannot alter rendered output.
func (e *Engine) describe() (*wasm.PluginDescriptor, error) {
	if e.descriptor != nil || e.descriptorErr != nil {
		return e.descriptor, e.descriptorErr
	}
	resolved, err := e.lookup(e.pluginURI)
	if err != nil {
		e.descriptorErr = fmt.Errorf("wasmengine: resolve plugin %q: %w", e.pluginURI, err)
		return nil, e.descriptorErr
	}
	plugin, desc, err := e.loader.LoadAndDescribe(context.Background(), resolver.PluginSpec(e.pluginURI, resolved))
	if err != nil {
		e.descriptorErr = err
		return nil, e.descriptorErr
	}
	_ = plugin.Close(context.Background())
	e.descriptor = &desc
	return e.descriptor, nil
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

	listing, err := enumerateSource(src)
	if err != nil {
		return nil, err
	}

	input := renderer.RenderNodeInput{
		Node:          e.nodeProjection(nodeCtx, valuesToOrdered(values)),
		Context:       e.renderContext(nodeCtx),
		SourceListing: listing,
	}
	out, err := e.invoke(ctx, RenderNodeExport, input, src)
	if err != nil {
		return nil, err
	}
	if err := e.surfaceNodeDiagnostics(nodeCtx, out.Diagnostics); err != nil {
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
// Each emitted file carries the Intermediate marking classified from the guest's
// boundary-assembly self-report (renderer.RenderIsIntermediate): a fragment-merging
// provider (compose) marks its fragments intermediate so the orchestrator routes
// them to AssembleBoundary rather than writing them at the dispatch path, while a
// nesting provider (helm-chart) and a flat-output provider (helm) mark them false.
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
	out, err := e.invoke(ctx, RenderNodeExport, input, "")
	if err != nil {
		return nil, err
	}
	if err := e.surfaceNodeDiagnostics(nodeCtx, out.Diagnostics); err != nil {
		return nil, err
	}

	intermediate := renderer.RenderIsIntermediate(e)
	emitted := make([]renderer.EmittedFile, 0, len(out.Files))
	for _, f := range out.Files {
		emitted = append(emitted, renderer.EmittedFile{Name: f.Path, Body: f.Content, Intermediate: intermediate})
	}
	return emitted, nil
}

// EmitArtifacts projects the node's pre-evaluated artifact set into a render_node
// payload and returns the guest's placed output. The host has already read each
// entry's source, evaluated it as a Go template with the guest's declared
// delimiter pair, and computed the build-provenance key sets; the guest decides
// the output format, the provenance placement, the output path, and the final
// bytes. Emission order is deterministic and load-bearing (the projection carries
// entries in author order; the guest preserves it), so the guest's file order is
// preserved — never re-sorted.
//
// The input-kind origin of each placed file is reconstructed by base-name match
// against the projection's import-origin entries: a file whose base name is an
// imported entry's name is artifacts-import, and any other file — an inline entry
// or a guest-emitted companion such as terraform's standalone provenance file —
// is artifacts-inline, matching the host's prior classification. The Intermediate
// marking is classified from the guest's boundary-assembly self-report
// (renderer.RenderIsIntermediate): a fragment-merging provider (compose) marks its
// artifacts intermediate; a flat-output (helm, terraform) or nesting (helm-chart)
// provider marks them false.
func (e *Engine) EmitArtifacts(
	ctx context.Context,
	proj renderer.ArtifactsProjection,
	nodeCtx renderer.NodeRenderContext,
) ([]renderer.EmittedFile, error) {
	input := renderer.RenderNodeInput{
		Node:                e.nodeProjection(nodeCtx, types.OrderedMap[any]{}),
		Context:             e.renderContext(nodeCtx),
		ArtifactsProjection: &proj,
	}
	out, err := e.invoke(ctx, RenderNodeExport, input, "")
	if err != nil {
		return nil, err
	}
	if err := e.surfaceNodeDiagnostics(nodeCtx, out.Diagnostics); err != nil {
		return nil, err
	}

	importNames := make(map[string]bool, len(proj.Entries))
	for _, ent := range proj.Entries {
		if ent.Origin == "import" {
			importNames[ent.Name] = true
		}
	}

	intermediate := renderer.RenderIsIntermediate(e)
	emitted := make([]renderer.EmittedFile, 0, len(out.Files))
	for _, f := range out.Files {
		origin := renderer.EmitOriginArtifactsInline
		if importNames[filepath.Base(f.Path)] {
			origin = renderer.EmitOriginArtifactsImport
		}
		emitted = append(emitted, renderer.EmittedFile{
			Name:         f.Path,
			Origin:       origin,
			Body:         f.Content,
			Intermediate: intermediate,
		})
	}
	return emitted, nil
}

// ArtifactTemplateDelimiters reports the Go-template delimiter pair the host
// evaluates the node's artifact entries with — the guest's own declared pair from
// its describe() self-report (capabilities.artifact_delimiters), defaulting to the
// "{{"/"}}" pair when the guest declares none, so a guest whose target syntax
// collides with that pair (helm-chart's "[["/"]]") has its downstream syntax left
// untouched by the host's pre-templating pass.
func (e *Engine) ArtifactTemplateDelimiters() (left, right string) {
	desc, err := e.describe()
	if err != nil || desc.Capabilities.ArtifactDelimiters == nil {
		return "{{", "}}"
	}
	return desc.Capabilities.ArtifactDelimiters.Left, desc.Capabilities.ArtifactDelimiters.Right
}

// Warm compiles the provider guest into the shared loader's compilation cache
// without rendering a node, so a later per-node load is a cache hit rather than a
// cold compilation. It resolves the guest's bytes through the same lookup and
// loads through the same shared loader Render uses, then closes the instance
// immediately — only the cached compiled machine code (a pure function of the
// guest bytes) survives, so warming cannot alter rendered output. A resolution or
// load failure is returned unchanged: it is the same failure the first render
// load would raise. AssemblerEngine embeds *Engine and inherits this method.
func (e *Engine) Warm(ctx context.Context) error {
	resolved, err := e.lookup(e.pluginURI)
	if err != nil {
		return fmt.Errorf("wasmengine: resolve plugin %q: %w", e.pluginURI, err)
	}
	plugin, desc, err := e.loader.LoadAndDescribe(ctx, resolver.PluginSpec(e.pluginURI, resolved))
	if err != nil {
		return err
	}
	if e.descriptor == nil {
		e.descriptor = &desc
	}
	return plugin.Close(ctx)
}

// invoke loads a fresh guest instance through the shared loader, calls the named
// export against the Engine's cached descriptor, and decodes the RenderNodeOutput.
// The shared loader carries the build's external-input service and its compilation
// cache, so a guest fetch resolves through the pinned channel (nil when none is
// attached — the guest then receives the in-band not-available envelope) and the
// guest's machine code compiles once per build.
//
// The descriptor read here is the Engine's memo (describe): the describe() call,
// its validation, and the load-gate cross-checks are a pure function of the guest
// bytes, so they run once per provider rather than once per node — validating them
// per node would be redundant work with byte-identical results. The per-node load
// is therefore a plain Load (fresh instance) rather than a LoadAndDescribe.
//
// Isolation guarantee: each call loads and closes its own plugin instance, so no
// guest state carries across nodes by construction — the memo caches only the
// descriptor (pure data), never a live instance. The plugin is closed before
// invoke returns.
func (e *Engine) invoke(ctx context.Context, export string, input renderer.RenderNodeInput, sourceRoot string) (renderer.RenderNodeOutput, error) {
	desc, err := e.describe()
	if err != nil {
		return renderer.RenderNodeOutput{}, err
	}

	resolved, err := e.lookup(e.pluginURI)
	if err != nil {
		return renderer.RenderNodeOutput{}, fmt.Errorf("wasmengine: resolve plugin %q: %w", e.pluginURI, err)
	}

	plugin, err := e.loader.Load(ctx, resolver.PluginSpec(e.pluginURI, resolved))
	if err != nil {
		return renderer.RenderNodeOutput{}, err
	}
	defer plugin.Close(ctx)

	// The pack-source and read-source-file host functions resolve the guest's
	// selections under this node's acquired source. The root is set per invocation
	// rather than captured at load because a plugin instance may render many nodes,
	// each with its own source; an empty root disables source packing (the synthesis
	// path passes none).
	plugin.SetSourceRoot(sourceRoot)

	payload, err := input.MarshalJSON()
	if err != nil {
		return renderer.RenderNodeOutput{}, fmt.Errorf("wasmengine: marshal render_node input: %w", err)
	}
	raw, err := wasm.InvokeExport(ctx, plugin, *desc, export, payload)
	if err != nil {
		return renderer.RenderNodeOutput{}, err
	}

	var out renderer.RenderNodeOutput
	if err := out.UnmarshalJSON(raw); err != nil {
		return renderer.RenderNodeOutput{}, fmt.Errorf("wasmengine: decode render_node output: %w", err)
	}
	// Fold any guest-emitted warnings (aggrega_emit_warning host function) into
	// the output's diagnostics as warning-severity entries, so the two guest
	// advisory channels — structured output diagnostics and the emit-warning host
	// function — reach the caller through one list. Drained here, before the
	// deferred Close, so nothing is lost when the instance is torn down.
	for _, w := range plugin.DrainWarnings() {
		out.Diagnostics = append(out.Diagnostics, renderer.Diagnostic{Severity: "warning", Message: w})
	}
	return out, nil
}

// surfaceNodeDiagnostics routes a provider guest's render/synthesis diagnostics:
// warning- and info-severity entries go to the node's warning sink (nil-safe,
// each prefixed with the node and provider identity), and error-severity entries
// are joined into a returned render error naming the node and provider. Warnings
// never affect emitted bytes, so a node's rendered output is byte-identical
// whether or not a sink is attached.
func (e *Engine) surfaceNodeDiagnostics(nodeCtx renderer.NodeRenderContext, diags []renderer.Diagnostic) error {
	errs, advisories := renderer.SplitDiagnostics(diags)
	for _, a := range advisories {
		if nodeCtx.WarningSink != nil {
			nodeCtx.WarningSink(fmt.Sprintf("node %q (provider %q): %s", nodeCtx.NodeID, nodeCtx.ProviderName, a))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("wasmengine: node %q: provider %q reported render errors: %s",
			nodeCtx.NodeID, nodeCtx.ProviderName, strings.Join(errs, "; "))
	}
	return nil
}

// advertisesExport reports whether the guest's describe() self-report declares an
// export of the given name — the per-invocation gate for an optional export. The
// adapter drives an optional export only when the guest advertises it, so a guest
// that omits it is never invoked for that crossing.
func advertisesExport(desc *wasm.PluginDescriptor, name string) bool {
	for _, ex := range desc.Exports {
		if ex.Name == name {
			return true
		}
	}
	return false
}

// ValidateNodeInput drives the guest's optional validate_node_input export to
// structurally check a node's resolved input before render dispatch, returning
// the diagnostics the guest raised. It is advertised-only: a guest that does not
// declare the export in describe() is never invoked, and this returns no
// diagnostics and no error. The export consumes the same RenderNodeInput
// projection render_node receives; because a sourced node's source arrives as a
// name-only listing, the guest reconstructs the bytes it parses through the
// pack-source host function, so the per-invocation source root is set before the
// crossing exactly as it is for render. At the input-kind gate a sourced node has
// its source resolved (the listing is available); a synthesised or artifacts-only
// node carries no source at that point, so the validate input carries the node
// projection and render context alone.
func (e *Engine) ValidateNodeInput(ctx context.Context, values map[string]any, nodeCtx renderer.NodeRenderContext) ([]renderer.Diagnostic, error) {
	desc, err := e.describe()
	if err != nil {
		return nil, err
	}
	if !advertisesExport(desc, ValidateNodeInputExport) {
		return nil, nil
	}

	input := renderer.RenderNodeInput{
		Node:    e.nodeProjection(nodeCtx, valuesToOrdered(values)),
		Context: e.renderContext(nodeCtx),
	}
	src, _ := nodeCtx.Source.(string)
	if src != "" {
		listing, err := enumerateSource(src)
		if err != nil {
			return nil, err
		}
		input.SourceListing = listing
	}
	return e.invokeValidate(ctx, ValidateNodeInputExport, input, src)
}

// invokeValidate loads a fresh guest instance, calls the input-validation export
// against the Engine's cached descriptor, and decodes the ValidateNodeOutput. It
// mirrors invoke's memoized-descriptor, fresh-instance-per-crossing lifecycle and
// per-invocation source root (the validate export packs source bytes through the
// same host function render does), but decodes the validate wire shape — a
// diagnostics-only payload with no files. Guest-emitted warnings are folded into
// the returned diagnostics as warning-severity entries. The plugin is closed
// before invokeValidate returns.
func (e *Engine) invokeValidate(ctx context.Context, export string, input renderer.RenderNodeInput, sourceRoot string) ([]renderer.Diagnostic, error) {
	desc, err := e.describe()
	if err != nil {
		return nil, err
	}

	resolved, err := e.lookup(e.pluginURI)
	if err != nil {
		return nil, fmt.Errorf("wasmengine: resolve plugin %q: %w", e.pluginURI, err)
	}

	plugin, err := e.loader.Load(ctx, resolver.PluginSpec(e.pluginURI, resolved))
	if err != nil {
		return nil, err
	}
	defer plugin.Close(ctx)

	plugin.SetSourceRoot(sourceRoot)

	payload, err := input.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("wasmengine: marshal validate_node_input input: %w", err)
	}
	raw, err := wasm.InvokeExport(ctx, plugin, *desc, export, payload)
	if err != nil {
		return nil, err
	}

	var out renderer.ValidateNodeOutput
	if err := out.UnmarshalJSON(raw); err != nil {
		return nil, fmt.Errorf("wasmengine: decode validate_node_input output: %w", err)
	}
	diags := out.Diagnostics
	for _, w := range plugin.DrainWarnings() {
		diags = append(diags, renderer.Diagnostic{Severity: "warning", Message: w})
	}
	return diags, nil
}

// BoundaryEnvOverrides drives the guest's optional boundary_env_overrides export
// to compute per-consumer environment overrides from a boundary's internal uses
// edges and the driving feature's resolved infra, returning the map keyed
// consumer-nodeID -> {envVar: value}. It is advertised-only: a guest that does
// not declare the export in describe() is never invoked, and this returns a nil
// map and no error, so the host applies no overrides for that provider. The
// computation lives entirely in the guest; the host projects the edge context in
// and applies the returned overrides. AssemblerEngine embeds *Engine and
// inherits this method.
func (e *Engine) BoundaryEnvOverrides(ctx context.Context, internalUsesEdges []types.EnrichedEdge, infra types.OrderedMap[any]) (map[string]map[string]string, error) {
	desc, err := e.describe()
	if err != nil {
		return nil, err
	}
	if !advertisesExport(desc, BoundaryEnvOverridesExport) {
		return nil, nil
	}

	resolved, err := e.lookup(e.pluginURI)
	if err != nil {
		return nil, fmt.Errorf("wasmengine: resolve plugin %q: %w", e.pluginURI, err)
	}

	plugin, err := e.loader.Load(ctx, resolver.PluginSpec(e.pluginURI, resolved))
	if err != nil {
		return nil, err
	}
	defer plugin.Close(ctx)

	input := renderer.BoundaryEnvOverridesInput{InternalUsesEdges: internalUsesEdges, Infra: infra}
	payload, err := input.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("wasmengine: marshal boundary_env_overrides input: %w", err)
	}
	raw, err := wasm.InvokeExport(ctx, plugin, *desc, BoundaryEnvOverridesExport, payload)
	if err != nil {
		return nil, err
	}

	var out renderer.BoundaryEnvOverridesOutput
	if err := out.UnmarshalJSON(raw); err != nil {
		return nil, fmt.Errorf("wasmengine: decode boundary_env_overrides output: %w", err)
	}
	return out.Overrides, nil
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
			ProviderName:    nodeCtx.ProviderName,
			ProviderVersion: nodeCtx.ProviderVersion,
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

// enumerateSource walks the acquired source at root generically — with no
// domain knowledge — into a name-only listing the host hands a sourced provider
// guest. It reads no file content: it records the root, whether the root is a
// directory, and, for a directory root, every entry under it keyed by its
// slash-separated root-relative path with a directory flag. Entries are sorted
// by path so the listing is byte-identical across runs of identical input.
//
// A single-file source root (a lone compose document, one module file, an
// archive) yields an empty entry set and a false directory flag; the guest packs
// the root itself. The guest applies its own resolution logic over the listing
// and asks the host to pack a chosen selection through the pack-source host
// function, so no per-provider source knowledge lives in this enumerator.
func enumerateSource(root string) (*renderer.SourceListing, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("wasmengine: stat source %q: %w", root, err)
	}
	listing := &renderer.SourceListing{Root: root, RootIsDir: info.IsDir()}
	if !info.IsDir() {
		return listing, nil
	}

	var entries []renderer.SourceEntry
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		entries = append(entries, renderer.SourceEntry{Path: filepath.ToSlash(rel), IsDir: d.IsDir()})
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("wasmengine: enumerate source %q: %w", root, walkErr)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	listing.Entries = entries
	return listing, nil
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
