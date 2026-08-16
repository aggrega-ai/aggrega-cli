// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasmengine

import (
	"bytes"
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
// deliberately distinct from the whole-graph policy socket's validate
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
// renderer.Provider and renderer.WorkloadSynthesizer interfaces. It resolves the
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
	// instance caches a reused plugin instance when the guest's describe()
	// self-report declares reusable_instance: one instance then serves every
	// render, assemble, and validate crossing of the build rather than a fresh
	// instance per crossing. The instance is loaded lazily on the first crossing
	// that acquires it and closed once at build end via Close. A guest that does
	// not declare the claim leaves this nil and loads a fresh instance per
	// crossing, so a stateful guest stays isolated by construction. A reused
	// instance is safe because the render loop is a single sequential goroutine
	// and a reusable guest holds no state across crossings; the per-crossing
	// source root is still set before each crossing (see acquireInstance). The
	// descriptor is a pure function of the guest bytes, so reuse cannot alter
	// rendered output. instanceErr caches a load failure so a broken guest is
	// reported consistently rather than re-probed per crossing.
	instance    *wasm.Plugin
	instanceErr error
}

var (
	_ renderer.Provider                       = (*Engine)(nil)
	_ renderer.NodeInputValidator             = (*Engine)(nil)
	_ renderer.WorkloadSynthesizer            = (*Engine)(nil)
	_ renderer.ArtifactsEmitter               = (*Engine)(nil)
	_ renderer.BoundaryEnvOverrider           = (*Engine)(nil)
	_ renderer.BoundaryAssemblyReporter       = (*Engine)(nil)
	_ renderer.AssemblyNameAnnotationReporter = (*Engine)(nil)
	_ renderer.CodecProvider                  = (*Engine)(nil)
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
func (e *Engine) Contract() renderer.ProviderContract {
	desc, err := e.describe()
	if err != nil || desc.Contract == nil {
		return renderer.ProviderContract{}
	}
	ik := desc.Contract.InputKinds
	return renderer.ProviderContract{
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
	if err != nil || desc.Contract == nil {
		return ""
	}
	return desc.Contract.BoundaryAssembly
}

// AssemblyNameAnnotation reports the boundary annotation whose resolved value
// names the provider guest's assembled artifact, read from its live describe()
// self-report (capabilities.assembly_name_annotation): the annotation key and
// whether every contributing boundary must agree on its resolved value. The
// descriptor is loaded and cached on first use; an empty annotation (and false)
// is returned when the guest declares none or cannot be loaded or described. It
// satisfies renderer.AssemblyNameAnnotationReporter, so the guard and cascade
// read the guest's own claim.
func (e *Engine) AssemblyNameAnnotation() (annotation string, requireUnique bool) {
	desc, err := e.describe()
	if err != nil || desc.Contract == nil || desc.Contract.AssemblyNameAnnotation == nil {
		return "", false
	}
	a := desc.Contract.AssemblyNameAnnotation
	return a.Annotation, a.RequireUnique
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
	spec := resolver.PluginSpec(e.pluginURI, resolved)
	spec.PluginType = wasm.PluginTypeProvider
	plugin, desc, err := e.loader.LoadAndDescribe(context.Background(), spec)
	if err != nil {
		e.descriptorErr = err
		return nil, e.descriptorErr
	}
	_ = plugin.Close(context.Background())
	e.descriptor = &desc
	return e.descriptor, nil
}

// acquireInstance returns a plugin instance for one crossing plus a release
// function the caller defers. When the guest's describe() self-report declares
// reusable_instance, the instance is loaded once and cached on the Engine for
// the build's remaining crossings and release is a no-op — the shared instance
// is closed once at build end via Close. Otherwise a fresh instance is loaded
// per crossing and release closes it, preserving the isolation default for a
// guest that makes no reuse claim. The reuse decision reads the guest's own
// self-report, so a guest opts into reuse rather than the host assuming it. The
// caller sets the per-crossing source root on the returned instance before
// invoking, so a reused instance never carries a prior node's source root into
// the next crossing.
func (e *Engine) acquireInstance(ctx context.Context) (*wasm.Plugin, func(), error) {
	desc, err := e.describe()
	if err != nil {
		return nil, nil, err
	}
	if desc.Contract == nil || !desc.Contract.ReusableInstance {
		p, err := e.loadInstance(ctx)
		if err != nil {
			return nil, nil, err
		}
		return p, func() { _ = p.Close(ctx) }, nil
	}
	if e.instance != nil {
		return e.instance, func() {}, nil
	}
	if e.instanceErr != nil {
		return nil, nil, e.instanceErr
	}
	p, err := e.loadInstance(ctx)
	if err != nil {
		e.instanceErr = err
		return nil, nil, err
	}
	e.instance = p
	return e.instance, func() {}, nil
}

// loadInstance resolves the guest's bytes through the lookup and loads a fresh
// instance through the shared loader. It is the single load site both the
// fresh-per-crossing and reused-instance paths share.
func (e *Engine) loadInstance(ctx context.Context) (*wasm.Plugin, error) {
	resolved, err := e.lookup(e.pluginURI)
	if err != nil {
		return nil, fmt.Errorf("wasmengine: resolve plugin %q: %w", e.pluginURI, err)
	}
	return e.loader.Load(ctx, resolver.PluginSpec(e.pluginURI, resolved))
}

// Close releases the Engine's reused plugin instance, if it holds one. A guest
// that declares reusable_instance is loaded once per build and its single
// instance is closed here at build end; a guest with no reuse claim loads and
// closes a fresh instance per crossing and holds nothing, so Close is a no-op
// for it. Idempotent. AssemblerEngine embeds *Engine and inherits this method,
// so closing the assembler adapter closes the same reused instance its render
// crossings share.
func (e *Engine) Close(ctx context.Context) error {
	if e.instance == nil {
		return nil
	}
	err := e.instance.Close(ctx)
	e.instance = nil
	return err
}

// Render projects the sourced node into a render_node payload and returns the
// guest's per-file output verbatim — each file's path, bytes, and the format the
// guest declared it in. nodeCtx.Source must be the resolved filesystem path the
// orchestrator populates before dispatch; a non-string value is an orchestrator
// bug rather than authoring drift. A
// duplicate output path from the guest is a loud error rather than a silent
// last-writer-wins overwrite.
func (e *Engine) Render(ctx context.Context, values map[string]any, nodeCtx renderer.NodeRenderContext) ([]renderer.RenderedFile, error) {
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
	e.surfaceDerivedKeys(nodeCtx, out.DerivedKeys)

	seen := make(map[string]struct{}, len(out.Files))
	for _, f := range out.Files {
		if _, dup := seen[f.Path]; dup {
			return nil, fmt.Errorf("wasmengine: node %q: guest emitted duplicate output path %q", nodeCtx.NodeID, f.Path)
		}
		seen[f.Path] = struct{}{}
	}
	return out.Files, nil
}

// SynthesizeWorkload projects the evaluated workload and its attachments'
// resolved provides into a render_node payload, and returns the guest's
// synthesized manifests in emission order. Emission order is deterministic and
// load-bearing, so the guest's file order is preserved — never re-sorted. The
// attachment lookup comes from renderer.AttachmentProvidesFromContext, the one
// host projection every synthesizer surface builds it from, so a workload
// attachment reference resolves to the same emitted resource identity wherever
// it is read.
//
// Whether an attachment handles declared-sensitive material is not projected
// here. It rides the node projection's feature entry for the same attachment,
// under the same instance name, which is where a guest reads it.
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
			Workload:           *w,
			AttachmentProvides: renderer.AttachmentProvidesFromContext(tmplCtx),
		},
	}
	out, err := e.invoke(ctx, RenderNodeExport, input, "")
	if err != nil {
		return nil, err
	}
	if err := e.surfaceNodeDiagnostics(nodeCtx, out.Diagnostics); err != nil {
		return nil, err
	}
	e.surfaceDerivedKeys(nodeCtx, out.DerivedKeys)

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
	e.surfaceDerivedKeys(nodeCtx, out.DerivedKeys)

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
	if err != nil || desc.Contract == nil || desc.Contract.ArtifactDelimiters == nil {
		return "{{", "}}"
	}
	return desc.Contract.ArtifactDelimiters.Left, desc.Contract.ArtifactDelimiters.Right
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
	spec := resolver.PluginSpec(e.pluginURI, resolved)
	spec.PluginType = wasm.PluginTypeProvider
	plugin, desc, err := e.loader.LoadAndDescribe(ctx, spec)
	if err != nil {
		return err
	}
	if e.descriptor == nil {
		e.descriptor = &desc
	}
	return plugin.Close(ctx)
}

// invoke acquires a guest instance through the shared loader, calls the named
// export against the Engine's cached descriptor, and decodes the RenderNodeOutput.
// The shared loader carries the build's external-input service and its compilation
// cache, so a guest fetch resolves through the pinned channel (nil when none is
// attached — the guest then receives the in-band not-available envelope) and the
// guest's machine code compiles once per build.
//
// The descriptor read here is the Engine's memo (describe): the describe() call,
// its validation, and the load-gate cross-checks are a pure function of the guest
// bytes, so they run once per provider rather than once per node — validating them
// per node would be redundant work with byte-identical results.
//
// Instance lifecycle: acquireInstance returns either the Engine's reused instance
// (when the guest declares reusable_instance) or a fresh instance closed via the
// returned release. A guest that makes no reuse claim keeps the fresh-per-crossing
// isolation default, so no guest state carries across nodes by construction. The
// source root is set per crossing on the acquired instance, so a reused instance
// never carries a prior node's source root into the next crossing.
//
// When the build runs under verify-determinism, invoke renders the node a second
// time through the same instance and compares the emitted bytes; a guest whose two
// renders of identical input diverge halts the build (ErrRenderNondeterministic),
// naming the node and provider.
func (e *Engine) invoke(ctx context.Context, export string, input renderer.RenderNodeInput, sourceRoot string) (renderer.RenderNodeOutput, error) {
	desc, err := e.describe()
	if err != nil {
		return renderer.RenderNodeOutput{}, err
	}

	plugin, release, err := e.acquireInstance(ctx)
	if err != nil {
		return renderer.RenderNodeOutput{}, err
	}
	defer release()

	// The pack-source and read-source-file host functions resolve the guest's
	// selections under this node's acquired source. The root is set per crossing
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
	if wasm.VerifyDeterminismFromContext(ctx) {
		if err := e.verifyRenderDeterminism(ctx, plugin, *desc, export, payload, raw, input.Node); err != nil {
			return renderer.RenderNodeOutput{}, err
		}
	}

	var out renderer.RenderNodeOutput
	if err := out.UnmarshalJSON(raw); err != nil {
		return renderer.RenderNodeOutput{}, fmt.Errorf("wasmengine: decode render_node output: %w", err)
	}
	if err := wasm.GuestReturnedError(export, out.Error); err != nil {
		return renderer.RenderNodeOutput{}, fmt.Errorf("wasmengine: %w", err)
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

// verifyRenderDeterminism renders the node a second time through the same
// instance and payload and compares the emitted bytes against the first render.
// It is the determinism backstop the verify-determinism build mode runs on every
// render crossing: a guest whose two renders of identical input diverge — a
// stateful guest whose output depends on prior crossings, or a guest that is
// otherwise non-deterministic — halts the build with ErrRenderNondeterministic,
// naming the node and provider. A deterministic guest produces identical bytes
// and the check passes. The comparison is over the guest's raw emitted output —
// the same surface the rendered artifacts are carved from. The second render's
// guest-emitted warnings fold into the caller's normal drain; under this opt-in
// mode a warning a guest emits during render may surface twice, which does not
// affect emitted bytes.
func (e *Engine) verifyRenderDeterminism(ctx context.Context, plugin *wasm.Plugin, desc wasm.PluginDescriptor, export string, payload, first []byte, node renderer.IRNodeProjection) error {
	second, err := wasm.InvokeExport(ctx, plugin, desc, export, payload)
	if err != nil {
		return err
	}
	if !bytes.Equal(first, second) {
		return fmt.Errorf(
			"wasmengine: node %q (provider %q): two renders of identical input produced different output: %w",
			node.ID, node.Provenance.ProviderName, renderer.ErrRenderNondeterministic())
	}
	return nil
}

// surfaceNodeDiagnostics routes a provider guest's render/synthesis diagnostics:
// warning- and info-severity entries go to the node's warning sink (nil-safe,
// each prefixed with the node and provider identity), and error-severity entries
// are joined into a returned render error naming the node and provider. Warnings
// never affect emitted bytes, so a node's rendered output is byte-identical
// whether or not a sink is attached.
func (e *Engine) surfaceNodeDiagnostics(nodeCtx renderer.NodeRenderContext, diags []renderer.Diagnostic) error {
	var errs []string
	for _, d := range diags {
		if d.Severity == "error" {
			errs = append(errs, d.Message)
			continue
		}
		// Unsupported-field advisories route to the structured unsupported-field sink
		// (when one is attached), enriched with the node and provider so the
		// display and downstream consumers keep attribution. This keeps the class
		// separately suppressible by its stable code while other advisories flow to
		// the free-form warning sink unchanged. When no unsupported-field sink is
		// attached the class falls through to the warning sink, so it never goes
		// silent.
		if d.Code == renderer.UnsupportedFieldCode && nodeCtx.UnsupportedFieldSink != nil {
			if d.Location == nil {
				d.Location = &renderer.DiagnosticLocation{URI: nodeCtx.NodeID, Kind: nodeCtx.ProviderName}
			}
			nodeCtx.UnsupportedFieldSink(d)
			continue
		}
		if nodeCtx.WarningSink != nil {
			nodeCtx.WarningSink(fmt.Sprintf("node %q (provider %q): %s", nodeCtx.NodeID, nodeCtx.ProviderName, d.Message))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("wasmengine: node %q: provider %q reported render errors: %s",
			nodeCtx.NodeID, nodeCtx.ProviderName, strings.Join(errs, "; "))
	}
	return nil
}

// surfaceDerivedKeys forwards the names a guest declares it derived to the node's
// derived-key sink (nil-safe), verbatim and uninspected. It is the one place a
// provider engine hands a declaration to the host, so every render crossing
// declares through the same seam.
//
// Nothing here reads a space name or a key. The guest's declaration is carried
// through unchanged: the host's later check compares the strings for equality
// within a space, so a provider format is never parsed and a key's meaning is
// never learned. Forwarding an empty declaration would be indistinguishable from
// forwarding nothing, so a guest that declares nothing costs the sink no call.
func (e *Engine) surfaceDerivedKeys(nodeCtx renderer.NodeRenderContext, declared types.OrderedMap[[]string]) {
	if nodeCtx.DerivedKeySink == nil || declared.Len() == 0 {
		return
	}
	nodeCtx.DerivedKeySink(declared)
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

// invokeValidate acquires a guest instance, calls the input-validation export
// against the Engine's cached descriptor, and decodes the ValidateNodeOutput. It
// mirrors invoke's memoized-descriptor lifecycle and per-crossing source root (the
// validate export packs source bytes through the same host function render does):
// the acquired instance is the Engine's reused one when the guest declares
// reusable_instance, or a fresh instance the returned release closes otherwise. It
// decodes the validate wire shape — a diagnostics-only payload with no files.
// Guest-emitted warnings are folded into the returned diagnostics as
// warning-severity entries.
func (e *Engine) invokeValidate(ctx context.Context, export string, input renderer.RenderNodeInput, sourceRoot string) ([]renderer.Diagnostic, error) {
	desc, err := e.describe()
	if err != nil {
		return nil, err
	}

	plugin, release, err := e.acquireInstance(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

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
	if err := wasm.GuestReturnedError(export, out.Error); err != nil {
		return nil, fmt.Errorf("wasmengine: %w", err)
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

	plugin, release, err := e.acquireInstance(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

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

// LinksCrossBoundary reports whether the guest advertises the cross-boundary
// linking capability: its describe() self-report declares both the link_publish
// and link_resolve exports. The host reads it before routing any link crossing
// to the engine, so a guest that declares neither (or only one) export wires no
// cross-boundary edge. AssemblerEngine embeds *Engine and inherits this method.
func (e *Engine) LinksCrossBoundary() bool {
	desc, err := e.describe()
	if err != nil {
		return false
	}
	return advertisesExport(desc, wasm.LinkPublishExport) && advertisesExport(desc, wasm.LinkResolveExport)
}

// LinkPublish drives the guest's optional link_publish export to produce the
// producer-side wiring for a cross-boundary link — the published identity the
// consumer resolves against and any structural patches to apply to the consumed
// endpoint's assembled output. It is advertised-only: a guest that does not
// declare the export in describe() is never invoked, and this returns a zero
// output and no error, so the host applies no wiring for that provider. The
// wiring lives entirely in the guest; the host projects the per-edge payload in
// and applies the returned patches. AssemblerEngine embeds *Engine and inherits
// this method.
func (e *Engine) LinkPublish(ctx context.Context, in renderer.LinkPublishInput) (renderer.LinkPublishOutput, error) {
	desc, err := e.describe()
	if err != nil {
		return renderer.LinkPublishOutput{}, err
	}
	if !advertisesExport(desc, wasm.LinkPublishExport) {
		return renderer.LinkPublishOutput{}, nil
	}

	plugin, release, err := e.acquireInstance(ctx)
	if err != nil {
		return renderer.LinkPublishOutput{}, err
	}
	defer release()

	payload, err := in.MarshalJSON()
	if err != nil {
		return renderer.LinkPublishOutput{}, fmt.Errorf("wasmengine: marshal link_publish input: %w", err)
	}
	raw, err := wasm.InvokeExport(ctx, plugin, *desc, wasm.LinkPublishExport, payload)
	if err != nil {
		return renderer.LinkPublishOutput{}, err
	}

	var out renderer.LinkPublishOutput
	if err := out.UnmarshalJSON(raw); err != nil {
		return renderer.LinkPublishOutput{}, fmt.Errorf("wasmengine: decode link_publish output: %w", err)
	}
	return out, nil
}

// LinkResolve drives the guest's optional link_resolve export to produce the
// consumer-side wiring for a cross-boundary link — the structural patches that
// inject the reference to the peer's published identity into the consuming
// endpoint's assembled output. It is advertised-only: a guest that does not
// declare the export in describe() is never invoked, and this returns a zero
// output and no error, so the host applies no wiring for that provider. The
// wiring lives entirely in the guest; the host projects the per-edge payload
// plus the peer's published handle in and applies the returned patches.
// AssemblerEngine embeds *Engine and inherits this method.
func (e *Engine) LinkResolve(ctx context.Context, in renderer.LinkResolveInput) (renderer.LinkResolveOutput, error) {
	desc, err := e.describe()
	if err != nil {
		return renderer.LinkResolveOutput{}, err
	}
	if !advertisesExport(desc, wasm.LinkResolveExport) {
		return renderer.LinkResolveOutput{}, nil
	}

	plugin, release, err := e.acquireInstance(ctx)
	if err != nil {
		return renderer.LinkResolveOutput{}, err
	}
	defer release()

	payload, err := in.MarshalJSON()
	if err != nil {
		return renderer.LinkResolveOutput{}, fmt.Errorf("wasmengine: marshal link_resolve input: %w", err)
	}
	raw, err := wasm.InvokeExport(ctx, plugin, *desc, wasm.LinkResolveExport, payload)
	if err != nil {
		return renderer.LinkResolveOutput{}, err
	}

	var out renderer.LinkResolveOutput
	if err := out.UnmarshalJSON(raw); err != nil {
		return renderer.LinkResolveOutput{}, fmt.Errorf("wasmengine: decode link_resolve output: %w", err)
	}
	return out, nil
}

// nodeProjection builds the wire node projection from the host node context —
// the inverse of the guest's own context reconstruction. It carries the identity
// and provenance the render core stamps onto every emitted manifest, plus the
// node's attachments in author order: an attachment entry carries the derived
// sensitivity marking, which is what lets a guest render a target-native
// sensitivity primitive rather than guess from a value.
//
// Type, resolved recipe, and uses edges stay unset, each for its own reason. The
// component type is opaque metadata rather than a behavior switch, so carrying it
// would only invite a guest to branch on it. The bound Recipe's identity already
// reaches the guest as the recipe name and version it stamps into provenance, so
// the resolved URI adds nothing it renders from. And an outbound edge is
// consumed before dispatch: the edge's provides are rehydrated into the node's
// own inject pipe, which the Recipe has already evaluated into the values and
// workload the guest receives, while a boundary assembler that needs the topology
// itself gets the edge list on its own input.
func (e *Engine) nodeProjection(nodeCtx renderer.NodeRenderContext, values types.OrderedMap[any]) renderer.IRNodeProjection {
	return renderer.IRNodeProjection{
		ID:               nodeCtx.NodeID,
		Values:           values,
		Features:         nodeCtx.Attachments,
		BoundaryPath:     nodeCtx.BoundaryPath,
		SyncWave:         nodeCtx.SyncWave,
		RecipeName:       nodeCtx.RecipeName,
		RecipeVersion:    nodeCtx.RecipeVersion,
		LabelComponentID: nodeCtx.LabelComponentID,
		OutputLabels:     nodeCtx.OutputLabels,
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
// render-wide indentation width and the node's already-merged annotations placed
// in a single bucket in sorted key order, so the wire payload is deterministic
// and the guest's own bucket-merge yields that same flat map.
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
