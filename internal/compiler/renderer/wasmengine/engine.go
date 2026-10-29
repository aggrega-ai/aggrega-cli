// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasmengine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/sourcewalk"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The provider exports this adapter drives are the wasm package's identifiers for
// the names the provider plugin type's host-export list holds, so each name is
// spelled in that list alone. Every crossing below names the identifier of the
// export it calls where it calls it, so the invocation, the decode of the reply
// and any failure it reports all name one export.
//
// The render family's wire is canonical CBOR in both directions: each crossing
// encodes its input with the generated binary encoder and decodes the reply
// through the binary encoding's strict entry point, which refuses a reply that is
// not the shape the crossing reads — a top-level field the shape does not
// declare, a field of the wrong type, or bytes that are not a well-formed value.
// Each export is fixed by the provider type rather than chosen by an attachment,
// so no configuration can point a crossing at the wrong entry function; the
// strict decode is there so the plugin-wire registry, which converts an authored
// payload for each of these exports, is checked against the crossings that call
// them.

// RenderNodeExport is the guest export this adapter drives for both sourced
// render and workload synthesis; the helm provider guest routes on which
// projection the RenderNodeInput carries (source vs workload). It is exported so
// the dispatch that routes a provider to this adapter can require the catalog
// Plugin entity to advertise the same export the adapter will drive — one name,
// one source of truth, no drift between the routing precondition and the call.
var RenderNodeExport = wasm.RenderNodeExport

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
	_ renderer.NameBounder                    = (*Engine)(nil)
	_ renderer.RenderedNameReporter           = (*Engine)(nil)
	_ renderer.BoundaryAssemblyReporter       = (*Engine)(nil)
	_ renderer.OmitsReporter                  = (*Engine)(nil)
	_ renderer.BoundaryArtifactsReporter      = (*Engine)(nil)
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
//
// The field copy below SURVIVES consolidation for a structural reason: it crosses
// from the GENERATED describe-socket codec into the gate's contract type, and the
// socket's two halves cannot share a Go type because a guest must not import the
// host. The gate's type is itself an alias of the claim's one home,
// [types.PluginInputKinds], so this is the only copy left on this path.
//
// A kind missed here reads as "the guest does not render it" and the gate declines
// a node the guest could have rendered. Adding an input kind means editing this
// copy; the field-set guard in internal/wasm fails when the generated shape and the
// home type stop agreeing, which is what turns a forgotten line into a red. Do not
// declare another three-boolean struct.
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

// HonoursOmits reports whether the provider guest declares that it honours omit
// entries, read from its live describe() self-report (contract.omits). The
// descriptor is loaded and cached on first use; false is returned when the guest
// declares nothing or cannot be loaded or described, so a document declaring omits
// is refused rather than handed to a guest that would leave nothing out. It
// satisfies renderer.OmitsReporter, so the rules refusing a document whose provider
// does not honour its entries read the guest's own claim.
func (e *Engine) HonoursOmits() bool {
	desc, err := e.describe()
	if err != nil || desc.Contract == nil {
		return false
	}
	return desc.Contract.Omits
}

// AcceptsBoundaryArtifacts reports whether the provider guest declares that its
// target accepts the files a Recipe contributes to its boundary's shared output,
// read from its live describe() self-report (contract.boundary_artifacts). The
// descriptor is loaded and cached on first use; false is returned when the guest
// declares nothing or cannot be loaded or described, so a Recipe declaring such
// files is refused rather than rendered into an output its target ignores them in.
// It satisfies renderer.BoundaryArtifactsReporter, so the rule refusing a Recipe
// whose provider does not accept the files reads the guest's own claim.
func (e *Engine) AcceptsBoundaryArtifacts() bool {
	desc, err := e.describe()
	if err != nil || desc.Contract == nil {
		return false
	}
	return desc.Contract.BoundaryArtifacts
}

// NestSegment reports the directory the provider guest's target places each
// contributor's own sub-directory under, read from its live describe()
// self-report (contract.nest_segment). The descriptor is loaded and cached on
// first use; an empty directory is returned when the guest declares none or
// cannot be loaded or described. It satisfies renderer.NestSegmentReporter, so
// the orchestrator composes a group's layout from the guest's own claim.
func (e *Engine) NestSegment() string {
	desc, err := e.describe()
	if err != nil || desc.Contract == nil {
		return ""
	}
	return desc.Contract.NestSegment
}

// describe is the capability-query form of describeContext, for the engine's
// capability methods, whose contracts carry no context.
//
// Those methods are the engine's self-report surface — which input kinds it
// renders, how it assembles a boundary, what it nests under — and each is
// declared on a reporter interface the orchestrator, the gate and the pipeline
// read through. Giving any of them a context would reshape that interface and
// every implementation of it, including the doubles a test supplies, so the
// contextless form stays and the caller's context arrives through
// describeContext wherever a caller has one.
//
// The load is memoized, so it happens at most once per Engine: whichever caller
// reaches it first drives it, and every later query reads the cached descriptor.
// A render, a validation, a codec crossing and a link crossing all hold the
// run's context and call the Context form, so on a real build the load runs
// under the run's own context and a stop reaches it. This form drives the load
// only when a capability query is the first thing to ask.
func (e *Engine) describe() (*wasm.PluginDescriptor, error) {
	return e.describeContext(context.Background())
}

// describeContext loads the provider guest, reads and validates its describe()
// self-report, and caches the result (or the failure) on the Engine so the
// input-kind gate and the render classification read the guest's plugin-level
// capability claim without reloading. The descriptor is a pure function of the
// guest bytes, so caching it cannot alter rendered output.
//
// ctx is the caller's own, carried to the load and to the close that follows it.
// A guest call whose context ends unwinds rather than being noticed only once
// the guest next returns, so a describe that does not come back is stoppable.
func (e *Engine) describeContext(ctx context.Context) (*wasm.PluginDescriptor, error) {
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
	plugin, desc, err := e.loader.LoadAndDescribe(ctx, spec)
	if err != nil {
		e.descriptorErr = err
		return nil, e.descriptorErr
	}
	_ = plugin.Close(ctx)
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
	desc, err := e.describeContext(ctx)
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
//
// This is the one crossing that hands a guest omit entries: the node context's
// resolved entries ride the input's omits list, and the counts the guest returns
// for them are read through renderer.RequireOmitCounts as soon as the guest's own
// halting diagnostics have been reported, before anything the crossing returned is
// forwarded. A node with no entries sends none, so its payload is the one it would
// be without the key.
func (e *Engine) Render(ctx context.Context, values map[string]any, nodeCtx renderer.NodeRenderContext) ([]renderer.RenderedFile, error) {
	src, ok := nodeCtx.Source.(string)
	if !ok {
		return nil, fmt.Errorf(
			"wasmengine: node %q: internal error — Render dispatched with non-string source %T; the orchestrator resolves spec.source to a filesystem path before dispatch",
			nodeCtx.NodeID, nodeCtx.Source)
	}

	source := readSourceThrough(src, nodeCtx.SourceIgnore)
	listing, err := enumerateSource(source)
	if err != nil {
		return nil, err
	}

	input := renderer.RenderNodeInput{
		Node:          e.nodeProjection(nodeCtx, valuesToOrdered(values), nil),
		Context:       e.renderContext(nodeCtx),
		SourceListing: listing,
		Omits:         renderer.OmitEntriesOf(nodeCtx.Omits),
	}
	out, err := e.invoke(ctx, input, source)
	if err != nil {
		return nil, err
	}
	if err := e.surfaceNodeDiagnostics(nodeCtx, out.Diagnostics); err != nil {
		return nil, err
	}
	if err := renderer.RequireOmitCounts(nodeCtx.Omits, out.Omitted, nodeCtx.RecipeName, nodeCtx.NodeID, e.pluginURI); err != nil {
		return nil, err
	}
	e.surfaceDerivedKeys(nodeCtx, out.DerivedKeys)
	if err := e.surfaceRenderFacts(nodeCtx, out.RenderFacts); err != nil {
		return nil, err
	}

	seen := make(map[string]struct{}, len(out.Files))
	for _, f := range out.Files {
		if _, dup := seen[f.Path]; dup {
			return nil, renderer.DuplicateRenderPathErr(f.Path, nodeCtx.NodeID)
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
// under the same instance name, which is where a guest reads it. Whether the
// workload's own template read a value declared sensitive is projected here,
// beside the workload it describes, from the context the synthesis was handed.
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
		Node:    e.nodeProjection(nodeCtx, types.OrderedMap[any]{}, w),
		Context: e.renderContext(nodeCtx),
		WorkloadProjection: &renderer.WorkloadProjection{
			Workload:           *w,
			SensitiveWorkload:  nodeCtx.SensitiveWorkload,
			AttachmentProvides: renderer.AttachmentProvidesFromContext(tmplCtx),
		},
	}
	out, err := e.invoke(ctx, input, renderSource{})
	if err != nil {
		return nil, err
	}
	if err := e.surfaceNodeDiagnostics(nodeCtx, out.Diagnostics); err != nil {
		return nil, err
	}
	e.surfaceDerivedKeys(nodeCtx, out.DerivedKeys)
	if err := e.surfaceRenderFacts(nodeCtx, out.RenderFacts); err != nil {
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
// The input-kind origin of each placed file is read back from its path through
// [renderer.ArtifactOriginIndex], which matches the whole output name the
// projection carries and tolerates a directory the guest prepended: a file the
// index attributes to an imported entry is artifacts-import, one attributed to an
// inline entry is artifacts-inline, and a file no entry claims — a guest-emitted
// companion such as terraform's standalone provenance file — is artifacts-inline
// as well. The reading lives there rather than here so every adapter on the
// artifacts seam attributes a file the same way. The Intermediate
// marking is classified from the guest's boundary-assembly self-report
// (renderer.RenderIsIntermediate): a fragment-merging provider (compose) marks its
// artifacts intermediate; a flat-output (helm, terraform) or nesting (helm-chart)
// provider marks them false.
//
// Each placed file keeps the format the guest declared for it, empty where it
// declared none. The declaration is the guest's alone — this adapter supplies
// none of its own — and it travels with the file to the render policies, which
// choose a parser by it ahead of the file's extension, so a file a Recipe
// authored is read as the guest declared it, exactly as a file the guest
// rendered from a source is.
func (e *Engine) EmitArtifacts(
	ctx context.Context,
	proj renderer.ArtifactsProjection,
	nodeCtx renderer.NodeRenderContext,
) ([]renderer.EmittedFile, error) {
	input := renderer.RenderNodeInput{
		Node:                e.nodeProjection(nodeCtx, types.OrderedMap[any]{}, nil),
		Context:             e.renderContext(nodeCtx),
		ArtifactsProjection: &proj,
	}
	out, err := e.invoke(ctx, input, renderSource{})
	if err != nil {
		return nil, err
	}
	if err := e.surfaceNodeDiagnostics(nodeCtx, out.Diagnostics); err != nil {
		return nil, err
	}
	e.surfaceDerivedKeys(nodeCtx, out.DerivedKeys)
	if err := e.surfaceRenderFacts(nodeCtx, out.RenderFacts); err != nil {
		return nil, err
	}

	origins := renderer.NewArtifactOriginIndex(proj)

	intermediate := renderer.RenderIsIntermediate(e)
	emitted := make([]renderer.EmittedFile, 0, len(out.Files))
	for _, f := range out.Files {
		emitted = append(emitted, renderer.EmittedFile{
			Name:         f.Path,
			Origin:       origins.For(f.Path),
			Body:         f.Content,
			Intermediate: intermediate,
			Format:       f.Format,
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

// invoke acquires a guest instance through the shared loader, calls render_node
// against the Engine's cached descriptor, and decodes the RenderNodeOutput.
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
func (e *Engine) invoke(ctx context.Context, input renderer.RenderNodeInput, source renderSource) (renderer.RenderNodeOutput, error) {
	export := wasm.RenderNodeExport
	desc, err := e.describeContext(ctx)
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
	// path passes none). The scope is set with it, so a selection is read through
	// the rules recorded for this node's source and no other's.
	plugin.SetSource(source.root, source.scope)

	payload, err := input.EncodeCBOR()
	if err != nil {
		return renderer.RenderNodeOutput{}, fmt.Errorf("encode render_node input: %w", err)
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
	if err := wasm.DecodeGuestOutputCBOR(export.Name(), raw, &out); err != nil {
		return renderer.RenderNodeOutput{}, err
	}
	if err := wasm.GuestReturnedError(e.pluginURI, export.Name(), wasm.GuestFailure{
		Message:   out.Error,
		Code:      out.ConditionCode,
		ShortCode: out.ConditionShortCode,
	}); err != nil {
		return renderer.RenderNodeOutput{}, fmt.Errorf("wasmengine: %w", err)
	}
	if err := wasm.GuestDiagnosticSeverities(e.pluginURI, export.Name(), out.Diagnostics); err != nil {
		return renderer.RenderNodeOutput{}, fmt.Errorf("wasmengine: %w", err)
	}
	// Fold any guest-emitted warnings (aggrega_emit_warning host function) into
	// the output's diagnostics as warn-severity entries, so the two guest
	// advisory channels — structured output diagnostics and the emit-warning host
	// function — reach the caller through one list. Drained here, before the
	// deferred Close, so nothing is lost when the instance is torn down.
	for _, w := range plugin.DrainWarnings() {
		out.Diagnostics = append(out.Diagnostics, renderer.Diagnostic{Severity: abi.SeverityWarn, Message: w})
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
func (e *Engine) verifyRenderDeterminism(ctx context.Context, plugin *wasm.Plugin, desc wasm.PluginDescriptor, export wasm.HostExport, payload, first []byte, node renderer.IRNodeProjection) error {
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
// warn- and info-severity entries go to the node's warning sink (nil-safe, each
// located at the node and provider), and halt-severity entries are joined into a
// returned render error naming the node and provider. Warnings never affect
// emitted bytes, so a node's rendered output is byte-identical whether or not a
// sink is attached.
//
// Both advisory routes are located identically and by one call, because the node
// and the provider are the same two coordinates whichever class an entry belongs
// to — a guest that sets its own location keeps it, since it knows something
// about where the fault sits that this crossing does not. The entry itself
// travels to each sink whole: what a guest reported about severity, class, the
// element concerned and any remediation it offered is what a consumer reading no
// terminal has to work from, and composing it into a line here would leave that
// consumer parsing a sentence to get it back.
func (e *Engine) surfaceNodeDiagnostics(nodeCtx renderer.NodeRenderContext, diags []renderer.Diagnostic) error {
	var errs []string
	for _, d := range diags {
		if d.Severity == abi.SeverityHalt {
			errs = append(errs, d.Message)
			continue
		}
		if d.Location == nil {
			d.Location = &renderer.DiagnosticLocation{URI: nodeCtx.NodeID, Kind: nodeCtx.ProviderName}
		}
		// Unsupported-field advisories route to the structured unsupported-field
		// sink when one is attached, which is what keeps the class separately
		// suppressible by its stable code while every other advisory reaches the
		// unconditional stream. When no unsupported-field sink is attached the
		// class falls through to the warning sink, so it never goes silent.
		if d.Code == renderer.UnsupportedFieldCode && nodeCtx.UnsupportedFieldSink != nil {
			nodeCtx.UnsupportedFieldSink(d)
			continue
		}
		if nodeCtx.WarningSink != nil {
			nodeCtx.WarningSink(d)
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

// surfaceRenderFacts forwards the facts a guest reports about what it emitted to
// the node's render-fact sink (nil-safe), verbatim and in the guest's own order.
// It is the one place a provider engine hands that report to the host, so every
// render crossing reports through the same seam.
//
// Every fact's value has to be a list, and a report carrying one that is not is
// refused here, before anything is forwarded, naming the fact and this guest —
// whether or not anything would have read the fact, because the report is the
// guest's whatever reads it. That a value is a list is the one thing read of it;
// nothing here reads a name or looks inside a list. The decode already
// copies each value out of the guest's reply; the forward copies it again so the
// sink never depends on how a reply was decoded, because that reply also holds
// every file the guest emitted and a fact kept after the crossing must not keep
// them alive. A guest that reports nothing costs the sink no call.
func (e *Engine) surfaceRenderFacts(nodeCtx renderer.NodeRenderContext, reported []renderer.RenderFact) error {
	if err := renderer.RequireRenderFactLists(reported, nodeCtx.NodeID, e.pluginURI); err != nil {
		return err
	}
	if nodeCtx.RenderFactSink == nil || len(reported) == 0 {
		return nil
	}
	forwarded := make([]renderer.RenderFact, len(reported))
	for i, fact := range reported {
		forwarded[i] = renderer.RenderFact{Name: fact.Name, Value: bytes.Clone(fact.Value)}
	}
	nodeCtx.RenderFactSink(forwarded)
	return nil
}

// advertisesExport reports whether the guest's describe() self-report declares
// export — the per-invocation gate for an optional export. The adapter drives an
// optional export only when the guest advertises it, so a guest that omits it is
// never invoked for that crossing.
func advertisesExport(desc *wasm.PluginDescriptor, export wasm.HostExport) bool {
	for _, ex := range desc.Exports {
		if ex.Name == export.Name() {
			return true
		}
	}
	return false
}

// ValidateNodeInput drives the guest's optional validate_node_input export to
// structurally check a node's resolved input before render dispatch, returning
// the diagnostics the guest raised. It is advertised-only: a guest that does not
// declare the export in describe() is never invoked, and this returns no
// diagnostics and no error, so the input-validation gate is a no-op for it. The
// export is deliberately distinct from the whole-graph policy socket's validation:
// it is per-node, provider-scoped, and consumes the same RenderNodeInput
// projection render_node receives; because a sourced node's source arrives as a
// name-only listing, the guest reconstructs the bytes it parses through the
// pack-source host function, so the per-invocation source root is set before the
// crossing exactly as it is for render. At the input-kind gate a sourced node has
// its source resolved (the listing is available); a synthesized or artifacts-only
// node carries no source at that point, so the validate input carries the node
// projection and render context alone.
func (e *Engine) ValidateNodeInput(ctx context.Context, values map[string]any, nodeCtx renderer.NodeRenderContext) ([]renderer.Diagnostic, error) {
	desc, err := e.describeContext(ctx)
	if err != nil {
		return nil, err
	}
	if !advertisesExport(desc, wasm.ValidateNodeInputExport) {
		return nil, nil
	}

	input := renderer.RenderNodeInput{
		Node:    e.nodeProjection(nodeCtx, valuesToOrdered(values), nil),
		Context: e.renderContext(nodeCtx),
	}
	src, _ := nodeCtx.Source.(string)
	var source renderSource
	if src != "" {
		source = readSourceThrough(src, nodeCtx.SourceIgnore)
		listing, err := enumerateSource(source)
		if err != nil {
			return nil, err
		}
		input.SourceListing = listing
	}
	return e.invokeValidate(ctx, input, source)
}

// invokeValidate acquires a guest instance, calls validate_node_input against
// the Engine's cached descriptor, and decodes the ValidateNodeOutput. It
// mirrors invoke's memoized-descriptor lifecycle and per-crossing source root (the
// validate export packs source bytes through the same host function render does):
// the acquired instance is the Engine's reused one when the guest declares
// reusable_instance, or a fresh instance the returned release closes otherwise. It
// decodes the validate wire shape — a diagnostics-only payload with no files.
// Guest-emitted warnings are folded into the returned diagnostics as
// warn-severity entries.
func (e *Engine) invokeValidate(ctx context.Context, input renderer.RenderNodeInput, source renderSource) ([]renderer.Diagnostic, error) {
	export := wasm.ValidateNodeInputExport
	desc, err := e.describeContext(ctx)
	if err != nil {
		return nil, err
	}

	plugin, release, err := e.acquireInstance(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	plugin.SetSource(source.root, source.scope)

	payload, err := input.EncodeCBOR()
	if err != nil {
		return nil, fmt.Errorf("encode validate_node_input input: %w", err)
	}
	raw, err := wasm.InvokeExport(ctx, plugin, *desc, export, payload)
	if err != nil {
		return nil, err
	}

	var out renderer.ValidateNodeOutput
	if err := wasm.DecodeGuestOutputCBOR(export.Name(), raw, &out); err != nil {
		return nil, err
	}
	if err := wasm.GuestReturnedError(e.pluginURI, export.Name(), wasm.GuestFailure{
		Message:   out.Error,
		Code:      out.ConditionCode,
		ShortCode: out.ConditionShortCode,
	}); err != nil {
		return nil, fmt.Errorf("wasmengine: %w", err)
	}
	if err := wasm.GuestDiagnosticSeverities(e.pluginURI, export.Name(), out.Diagnostics); err != nil {
		return nil, fmt.Errorf("wasmengine: %w", err)
	}
	diags := out.Diagnostics
	for _, w := range plugin.DrainWarnings() {
		diags = append(diags, renderer.Diagnostic{Severity: abi.SeverityWarn, Message: w})
	}
	return diags, nil
}

// NameBound drives the guest's optional name_bound export to ask whether a
// proposed component identity yields names its target accepts, returning the
// guest's per-name-kind verdicts.
//
// It is advertised-only: a guest that does not declare the export in describe()
// is never invoked, and this returns an empty output and no error — an empty
// verdict list is an acceptance, so such a provider's names are settled exactly
// as they were before the export existed. That guard is what makes the addition
// a no-op for every shipped and third-party guest that has not implemented it,
// and it is why the export must never join a minimum-export set: a minimum
// export is a LOAD-time requirement, and requiring this one would stop every
// existing provider plugin from loading at all.
//
// The guest answers with a constraint and never with a name it built. A name
// travelling back across this boundary would put the host in the business of
// carrying provider-specific names, which is the coupling asking the question
// removes. AssemblerEngine embeds *Engine and inherits this method.
func (e *Engine) NameBound(ctx context.Context, in renderer.NameBoundInput) (renderer.NameBoundOutput, error) {
	export := wasm.NameBoundExport
	desc, err := e.describeContext(ctx)
	if err != nil {
		return renderer.NameBoundOutput{}, err
	}
	if !advertisesExport(desc, export) {
		return renderer.NameBoundOutput{}, nil
	}

	plugin, release, err := e.acquireInstance(ctx)
	if err != nil {
		return renderer.NameBoundOutput{}, err
	}
	defer release()

	payload, err := in.EncodeCBOR()
	if err != nil {
		return renderer.NameBoundOutput{}, fmt.Errorf("encode name_bound input: %w", err)
	}
	raw, err := wasm.InvokeExport(ctx, plugin, *desc, export, payload)
	if err != nil {
		return renderer.NameBoundOutput{}, err
	}

	var out renderer.NameBoundOutput
	if err := wasm.DecodeGuestOutputCBOR(export.Name(), raw, &out); err != nil {
		return renderer.NameBoundOutput{}, err
	}
	if err := wasm.GuestReturnedError(e.pluginURI, export.Name(), wasm.GuestFailure{
		Message:   out.Error,
		Code:      out.ConditionCode,
		ShortCode: out.ConditionShortCode,
	}); err != nil {
		return renderer.NameBoundOutput{}, fmt.Errorf("wasmengine: %w", err)
	}
	return out, nil
}

// RenderedName drives the guest's optional rendered_name export to ask what name
// its target will give the resources it renders for a node, returning the guest's
// answer.
//
// It is advertised-only: a guest that does not declare the export in describe()
// is never invoked, and this returns an empty output and no error — which is the
// same answer a guest that derives no such name gives, and the same consequence:
// a recipe author has no such name to read for that node. It stays out of every
// minimum-export rule for the reason [Engine.NameBound]'s export does.
//
// The direction is the reverse of [Engine.NameBound] and the distinction is the
// whole of what keeps this legal. There the host proposes a name and the guest
// answers with a constraint, because a name travelling back would put the host in
// the business of composing names for a target it knows nothing about. Here the
// guest composed the name and the host carries the string without reading it, so
// nothing about the target's naming crosses into the core: no alphabet, no
// grammar, no length. AssemblerEngine embeds *Engine and inherits this method.
func (e *Engine) RenderedName(ctx context.Context, in renderer.RenderedNameInput) (renderer.RenderedNameOutput, error) {
	export := wasm.RenderedNameExport
	desc, err := e.describeContext(ctx)
	if err != nil {
		return renderer.RenderedNameOutput{}, err
	}
	if !advertisesExport(desc, export) {
		return renderer.RenderedNameOutput{}, nil
	}

	plugin, release, err := e.acquireInstance(ctx)
	if err != nil {
		return renderer.RenderedNameOutput{}, err
	}
	defer release()

	payload, err := in.EncodeCBOR()
	if err != nil {
		return renderer.RenderedNameOutput{}, fmt.Errorf("encode rendered_name input: %w", err)
	}
	raw, err := wasm.InvokeExport(ctx, plugin, *desc, export, payload)
	if err != nil {
		return renderer.RenderedNameOutput{}, err
	}

	var out renderer.RenderedNameOutput
	if err := wasm.DecodeGuestOutputCBOR(export.Name(), raw, &out); err != nil {
		return renderer.RenderedNameOutput{}, err
	}
	if err := wasm.GuestReturnedError(e.pluginURI, export.Name(), wasm.GuestFailure{
		Message:   out.Error,
		Code:      out.ConditionCode,
		ShortCode: out.ConditionShortCode,
	}); err != nil {
		return renderer.RenderedNameOutput{}, fmt.Errorf("wasmengine: %w", err)
	}
	return out, nil
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
	export := wasm.LinkPublishExport
	desc, err := e.describeContext(ctx)
	if err != nil {
		return renderer.LinkPublishOutput{}, err
	}
	if !advertisesExport(desc, export) {
		return renderer.LinkPublishOutput{}, nil
	}

	plugin, release, err := e.acquireInstance(ctx)
	if err != nil {
		return renderer.LinkPublishOutput{}, err
	}
	defer release()

	payload, err := in.EncodeCBOR()
	if err != nil {
		return renderer.LinkPublishOutput{}, fmt.Errorf("encode link_publish input: %w", err)
	}
	raw, err := wasm.InvokeExport(ctx, plugin, *desc, export, payload)
	if err != nil {
		return renderer.LinkPublishOutput{}, err
	}

	var out renderer.LinkPublishOutput
	if err := wasm.DecodeGuestOutputCBOR(export.Name(), raw, &out); err != nil {
		return renderer.LinkPublishOutput{}, err
	}
	if err := wasm.GuestReturnedError(e.pluginURI, export.Name(), wasm.GuestFailure{
		Message:   out.Error,
		Code:      out.ConditionCode,
		ShortCode: out.ConditionShortCode,
	}); err != nil {
		return renderer.LinkPublishOutput{}, fmt.Errorf("wasmengine: %w", err)
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
	export := wasm.LinkResolveExport
	desc, err := e.describeContext(ctx)
	if err != nil {
		return renderer.LinkResolveOutput{}, err
	}
	if !advertisesExport(desc, export) {
		return renderer.LinkResolveOutput{}, nil
	}

	plugin, release, err := e.acquireInstance(ctx)
	if err != nil {
		return renderer.LinkResolveOutput{}, err
	}
	defer release()

	payload, err := in.EncodeCBOR()
	if err != nil {
		return renderer.LinkResolveOutput{}, fmt.Errorf("encode link_resolve input: %w", err)
	}
	raw, err := wasm.InvokeExport(ctx, plugin, *desc, export, payload)
	if err != nil {
		return renderer.LinkResolveOutput{}, err
	}

	var out renderer.LinkResolveOutput
	if err := wasm.DecodeGuestOutputCBOR(export.Name(), raw, &out); err != nil {
		return renderer.LinkResolveOutput{}, err
	}
	if err := wasm.GuestReturnedError(e.pluginURI, export.Name(), wasm.GuestFailure{
		Message:   out.Error,
		Code:      out.ConditionCode,
		ShortCode: out.ConditionShortCode,
	}); err != nil {
		return renderer.LinkResolveOutput{}, fmt.Errorf("wasmengine: %w", err)
	}
	return out, nil
}

// nodeProjection builds this dispatch's wire node projection. The construction
// itself has one home in the render core, beside both the host context it reads
// and the wire type it produces, so every render shape reaches a guest through
// one projection builder and none can carry a field another leaves unset.
//
// Each caller passes exactly what its own shape carries — the resolved values a
// sourced render consumes, the evaluated workload a synthesizing render consumes
// — because that is what lets the builder recover a rendered key's identity from
// a declaration for the shape at hand.
func (e *Engine) nodeProjection(nodeCtx renderer.NodeRenderContext, values types.OrderedMap[any], w *types.Workload) renderer.IRNodeProjection {
	return renderer.NodeProjection(nodeCtx, values, w)
}

// renderContext is the wire render context this engine hands its guest on every
// export that carries one: the node's guest context, built by the renderer's one
// constructor — the same one a pipeline step's context comes from — with the
// render-wide indentation width the guest serializes its output at.
func (e *Engine) renderContext(nodeCtx renderer.NodeRenderContext) renderer.RenderContext {
	rc := nodeCtx.GuestContext
	rc.YAMLIndent = nodeCtx.YAMLIndent
	return rc
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
//
// The listing is read through the source's scope — the ignore rules the entity's
// lock records — so an entry the rules leave out is never shown to the guest, and
// a directory they leave out is not entered. A symbolic link anywhere beneath the
// root is refused with the condition every other reading of an entity's files
// raises, before the guest is invoked. What remains is exactly what a publication
// of the entity carries under the root.
func enumerateSource(source renderSource) (*renderer.SourceListing, error) {
	root := source.root
	if err := sourcewalk.RefuseLink(root, source.show(root)); err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("wasmengine: stat source %q: %w", root, err)
	}
	listing := &renderer.SourceListing{Root: root, RootIsDir: info.IsDir()}
	if !info.IsDir() {
		return listing, nil
	}

	kept, walkErr := source.scope.Entries(root, func(rel string) string {
		return source.show(filepath.Join(root, filepath.FromSlash(rel)))
	})
	if walkErr != nil {
		if errors.Is(walkErr, sourcewalk.ErrSymbolicLinkInEntityFiles()) {
			return nil, walkErr
		}
		return nil, fmt.Errorf("wasmengine: enumerate source %q: %w", root, walkErr)
	}
	entries := make([]renderer.SourceEntry, 0, len(kept))
	for _, e := range kept {
		entries = append(entries, renderer.SourceEntry{Path: e.Path, IsDir: e.IsDir})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	listing.Entries = entries
	return listing, nil
}

// renderSource is one node's source as a guest crossing reads it: the directory
// or file the node's source resolved to, and the scope its files are read through.
// The zero value is no source at all, which is what a synthesis or an artifacts
// crossing hands a guest.
type renderSource struct {
	root  string
	scope sourcewalk.Scope
	// base is the directory the scope's rules are spelled against — the entity's
	// own directory for a local source — and empty where no rule applies.
	base string
}

// readSourceThrough pairs a resolved source root with the ignore rules recorded
// for it. A local source reads through the rules the entity's lock records,
// spelled against the entity's directory; any other source reads through no rule.
func readSourceThrough(root string, ignore renderer.SourceIgnore) renderSource {
	if ignore.EntityDirectory == "" {
		return renderSource{root: root, scope: sourcewalk.NewScope(root, nil)}
	}
	return renderSource{
		root:  root,
		scope: sourcewalk.NewScope(ignore.EntityDirectory, ignore.Rules),
		base:  ignore.EntityDirectory,
	}
}

// show spells a path under the source for a reader: relative to the entity's own
// directory where the source is a local one, since that is how its author knows
// the file, and as it stands otherwise.
func (s renderSource) show(abs string) string {
	if s.base != "" {
		if rel, err := filepath.Rel(s.base, abs); err == nil && filepath.IsLocal(rel) {
			return filepath.ToSlash(rel)
		}
	}
	return abs
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
