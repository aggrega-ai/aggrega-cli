// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// pipelineResult is the outcome of running one node's pipeline through the
// shared executor: the rewritten own emitted set (PATCH applied to its
// documents, EMIT side effects appended as EmitOriginPipeline files), the
// host-document modifications keyed by their original emitted path (a /host
// PATCH rewrite or a /host EMIT addition — empty when the step has no host),
// and the warnings raised along the way (validate-only notice, soft-failed
// optional steps, non-error guest diagnostics).
type pipelineResult struct {
	Own         []EmittedFile
	HostPatched map[string][]byte
	Warnings    []string
}

// DeferredPatch carries everything the post-assembly phase needs to replay a
// pipeline against an assembler node's merged documents. An assembler provider
// (compose) renders pre-assembly fragments per-node, so there is no discrete
// document for the guest to content-select while the node is processed: the
// pipeline is recorded here and replayed against the merged documents once
// assembly has produced them — neither halted nor dropped. The case is
// scope-symmetric. A component-scope FeatureRecipe whose host is an assembler
// defers its /host pipeline; a Recipe (or component-scope FeatureRecipe) whose
// OWN provider is an assembler defers its /own pipeline. The same record carries
// both: the replay mounts the merged document as both the own root and the host
// root (for an assembler node the merged document is the node's own output and,
// for an attached FeatureRecipe, the host surface), and routes each step's ops
// by the step's declared scope.
//
// Steps is the whole pipeline (the full step slice carried in one record so a
// mixed own+host pipeline replays in its declared step order — splitting by
// scope would break the ordering a later step depends on). WithCtx and RenderCtx
// are the exact contexts the per-node pass evaluated against, captured so the
// replayed `with` renders identically. OwnProvider is the provider rendering the
// node's own documents (the FeatureRecipe's or Recipe's spec.capability — the
// assembler for an own-on-assembler defer); HostProvider is the host component's
// recipe provider (the assembler for a host-on-assembler defer). Both drive
// per-scope codec selection and the per-scope compatibility check on replay; a
// scope a record's steps never touch leaves its provider empty. NodeID is the
// node; FRInstance names the attached FeatureRecipe ("" for a Recipe own
// pipeline). HostNodeOrder and FeatureOrderIndex are the A1 replay-ordering keys:
// the node's position in topology author order, then the pipeline's position in
// the node's FeatureOrder cascade — so when several deferred pipelines replay
// they layer in the same deterministic order the per-node cascade uses.
type DeferredPatch struct {
	Steps             []types.PipelineStep
	WithCtx           *types.TemplateContext
	RenderCtx         renderctx.RenderContext
	OwnProvider       string
	HostProvider      string
	NodeID            string
	FRInstance        string
	HostNodeOrder     int
	FeatureOrderIndex int
}

// pipelineFile pairs an emitted file with the codec's segmentation of its
// bytes into addressable units. units is nil for a file the codec rejected
// entirely (no addressable units): such a file passes through verbatim. A
// unit the codec produced may itself be opaque (no Tree); a PATCH against an
// opaque unit halts in the walk as a not-structured target.
type pipelineFile struct {
	file  pipelineFile0
	units []frp.Unit
}

// pipelineFile0 is the EmittedFile fields the executor carries through the
// fold-back: the original name, body, intermediate flag, and origin. Held by
// value so the working copy never aliases the caller's slice.
type pipelineFile0 struct {
	Name         string
	Body         []byte
	Origin       EmitOrigin
	Intermediate bool
}

// runNodePipeline executes a node's pipeline steps over the keyed document set
// built from its emitted documents, the single executor shared by every
// pipeline site — a Recipe (own only), a component-scope FeatureRecipe (own +
// host), and a boundary-scope FeatureRecipe (own only). The scope-shape
// difference lives entirely in the caller's arguments: own carries the node's
// own emitted documents; host carries the attached host component's documents
// (nil for a Recipe or a boundary-scope FeatureRecipe).
//
// It segments each emitted file into discrete keyed documents, builds the
// scope-rooted DocumentSet, binds a StepInvoker over the shared plugin-path
// resolver and the describe() gate, calls the pipeline walk with the
// renderer's EvalTemplate as the template seam, and folds the walk's result
// back: a patched document is re-serialized into its file byte-faithfully via
// ApplyBack, an own-scope EMIT becomes a new EmitOriginPipeline file, a
// host-scope modification is keyed by the host document's path for the
// orchestrator's host-doc write-back conduit.
//
// hostIsAssembler reports whether the host component's provider is an
// assembler (its documents are not discrete until assembly). A PATCH against a
// pre-assembly fragment — an own-scope document from an Intermediate emitted
// file, or any host-scope document when hostIsAssembler — halts with
// ErrPipelinePatchPreAssembly; the post-assembly phase re-runs such a PATCH.
// EMIT is additive and runs per-node for every provider.
//
// ownProvider is the provider rendering this node's own documents (the recipe's
// or FeatureRecipe's spec.capability); hostProvider is the provider rendering the
// host component's documents (empty when the call has no host — a Recipe or a
// boundary-scope FeatureRecipe). They drive the pre-walk compatibility check:
// each step's plugin declares an enumerated set of compatible providers, and the
// provider of every scope the step's export touches must appear in it, or the
// build halts with ErrPluginProviderIncompatible before the walk runs any guest.
// The check reads each step plugin's describe() self-report to obtain that
// declared set, so it runs guest code itself; what it precedes is the walk.
//
// lookup nil disables execution: the steps are validated by their presence but
// not run, and a validate-only warning is returned (the non-regression path).
//
// descriptors is the build's retained set of step-plugin self-reports, so a
// plugin several nodes name is described once for the whole build rather than
// once per node. nil narrows retention to this call.
func runNodePipeline(
	ctx context.Context,
	steps []types.PipelineStep,
	own []EmittedFile,
	host []EmittedFile,
	hostIsAssembler bool,
	ownProvider string,
	hostProvider string,
	renderCtx renderctx.RenderContext,
	withCtx *types.TemplateContext,
	yamlIndent int,
	lookup pluginpath.Lookup,
	descriptors *stepDescriptorMemo,
	engineFactory EngineFactory,
	nodeID string,
) (pipelineResult, error) {
	res := pipelineResult{Own: own}
	if len(steps) == 0 {
		return res, nil
	}
	if lookup == nil {
		res.Warnings = append(res.Warnings,
			fmt.Sprintf("node %q: pipeline (%d steps) validated but not executed (no plugin path resolver configured)", nodeID, len(steps)))
		return res, nil
	}

	// Select the per-scope codec from each scope's rendering provider: the own
	// engine's codec carves the node's own documents, the host engine's codec
	// carves the host component's documents. A provider that implements
	// CodecProvider supplies its own units (a templated format, for instance);
	// every other provider borrows the stock YAML codec. Provider-name → engine
	// → codec routes through the same engineFactory the orchestrator uses, so
	// codec selection single-sources the provider→engine mapping.
	ownCodec := selectCodec(engineFactory, ownProvider, yamlIndent)
	hostCodec := selectCodec(engineFactory, hostProvider, yamlIndent)

	// Build the StepInvoker and the per-step descriptor accessor over the shared
	// resolver + describe() gate, then delegate to the executor body. The public
	// function owns only seam construction (and the empty-steps / nil-lookup
	// short-circuits above); the executor body is single-sourced in
	// runNodePipelineWithInvoker, which a test exercises with canned seams.
	return runNodePipelineWithInvoker(ctx, steps, own, host, hostIsAssembler, ownProvider, hostProvider, renderCtx, withCtx, ownCodec, hostCodec, newStepInvoker(lookup), newStepDescriber(descriptors, lookup), nodeID)
}

// selectCodec resolves the codec for the scope rendered by providerName: the
// provider's own codec when its engine reports one (the provider guest
// advertises the carve and reassemble exports), otherwise the stock YAML codec.
// An empty providerName (a scope the call has no provider for, such as the host
// scope of a hostless step) or a provider whose engine does not resolve falls
// back to the stock codec — the borrow default — so the returned codec is never
// nil.
func selectCodec(engineFactory EngineFactory, providerName string, yamlIndent int) frp.Codec {
	if providerName != "" && engineFactory != nil {
		if engine, err := engineFactory(providerName); err == nil {
			if cp, ok := engine.(CodecProvider); ok {
				if codec, has := cp.PipelineCodec(yamlIndent); has && codec != nil {
					return codec
				}
			}
		}
	}
	return newStockYAMLCodec(yamlIndent)
}

// runNodePipelineWithInvoker is the executor body shared by every caller that
// runs a pipeline: the per-node path, the post-assembly replay, and tests with
// canned seams. It runs the pre-walk provider-compatibility check, builds the
// keyed document set, walks the steps through invoke, and folds the result back
// — the one place enforcement, the segmenter, the walk, and apply-back are wired
// together.
//
// Two production callers reach it, and what differs between them is only where
// the seams come from: runNodePipeline builds invoke and describe per call from
// a plugin-path lookup, while the post-assembly replay builds them once and
// reuses them across every record it replays. Both seams must be non-nil.
func runNodePipelineWithInvoker(
	ctx context.Context,
	steps []types.PipelineStep,
	own []EmittedFile,
	host []EmittedFile,
	hostIsAssembler bool,
	ownProvider string,
	hostProvider string,
	renderCtx renderctx.RenderContext,
	withCtx *types.TemplateContext,
	ownCodec frp.Codec,
	hostCodec frp.Codec,
	invoke frp.StepInvoker,
	describe stepDescriber,
	nodeID string,
) (pipelineResult, error) {
	res := pipelineResult{Own: own}

	// The codec is the seam every carve + reassemble routes through, selected
	// per scope from the provider rendering that scope: ownCodec carves the
	// node's own documents, hostCodec the host component's. A provider that
	// implements CodecProvider supplies its own units; every other provider
	// borrows the stock YAML codec, which is node-span surgical so a patched
	// document edits only its own byte-spans and the surrounding
	// comments/anchors/styles survive.

	// 0. Pre-walk provider-compatibility enforcement. For each step, read the
	//    plugin's describe() descriptor and assert the provider rendering every
	//    scope the step's export touches is in the plugin's compatible providers.
	//    Fail-fast: a mismatch halts before the walk runs any guest.
	if err := enforceProviderCompatibility(ctx, steps, ownProvider, hostProvider, describe, nodeID); err != nil {
		return pipelineResult{}, err
	}

	// 1. Build the keyed unit set from the emitted files via each scope's codec.
	ownFiles, ownDocs, err := buildScopeDocs(ownCodec, own)
	if err != nil {
		return pipelineResult{}, wrapPipelineErr(err, nodeID)
	}
	hostFiles, hostDocs, err := buildScopeDocs(hostCodec, host)
	if err != nil {
		return pipelineResult{}, wrapPipelineErr(err, nodeID)
	}
	docs := frp.DocumentSet{Own: ownDocs, Host: hostDocs}

	// 3-4. Walk the steps. EvalTemplate is the injected template seam.
	walkRes, werr := frp.Walk(ctx, docs, steps, invoke, withCtx, renderCtx, EvalTemplate)
	if werr != nil {
		return pipelineResult{}, mapWalkError(werr, nodeID, len(host) > 0)
	}

	// 5. Fold the walk result back into emitted files.
	// 5a. Pre-assembly fragment guard (scope-symmetric). A PATCH that changed
	//     an own-scope document of an Intermediate file, or any host-scope
	//     document when the host provider is an assembler, halts — the target is
	//     a pre-assembly fragment.
	//
	//     For BOTH scopes this guard is the defensive floor, not the normal
	//     path. A pipeline against an assembler node — a component-scope
	//     FeatureRecipe whose host is an assembler (its /host pipeline), or a
	//     Recipe / component-scope FeatureRecipe whose OWN provider is an
	//     assembler (its /own pipeline) — is DEFERRED whole to the post-assembly
	//     phase before this executor runs against the fragments (the per-node set
	//     for the deferred scope is empty, so the guest cannot content-select a
	//     pre-assembly fragment). The guard therefore catches an assembler-scope
	//     PATCH only if one reaches the executor through a caller that did not
	//     defer — a bug it should halt loudly rather than apply against fragments
	//     assembly will discard. On the post-assembly replay hostIsAssembler is
	//     false and the documents are discrete, so neither branch fires.
	if step, ok := changedFragmentOwn(walkRes.AppliedOps.Own, ownFiles); ok {
		return pipelineResult{}, pipelinePreAssemblyErr(nodeID, step, "own")
	}
	if hostIsAssembler {
		if tok, ok := firstChangedToken(walkRes.AppliedOps.Host); ok {
			return pipelineResult{}, pipelinePreAssemblyErr(nodeID, tok, "host")
		}
	}

	// 5b. Apply PATCHes back into own files byte-faithfully via the own codec.
	ownOut, ownWarns, err := foldPatchesBack(ownCodec, ownFiles, walkRes.AppliedOps.Own, nodeID)
	if err != nil {
		return pipelineResult{}, err
	}
	res.Warnings = append(res.Warnings, ownWarns...)

	// 5c. Apply PATCHes back into host files via the host codec; surface them
	//     keyed by path.
	hostPatched := map[string][]byte{}
	if len(host) > 0 {
		hostOut, hostWarns, err := foldPatchesBack(hostCodec, hostFiles, walkRes.AppliedOps.Host, nodeID)
		if err != nil {
			return pipelineResult{}, err
		}
		res.Warnings = append(res.Warnings, hostWarns...)
		for _, f := range hostOut {
			if before, ok := lookupFile(hostFiles, f.Name); ok && !bytes.Equal(before.file.Body, f.Body) {
				hostPatched[f.Name] = f.Body
			}
		}
	}

	// 5d. EMIT side effects → new files, in step+effect order.
	for i, se := range walkRes.SideEffects {
		content, perr := sideEffectBytes(se, nodeID, i)
		if perr != nil {
			return pipelineResult{}, perr
		}
		name := sideEffectName(se, i)
		switch se.Scope {
		case frp.DocumentScopeHost:
			if len(host) == 0 {
				return pipelineResult{}, pipelineHostScopeErr(nodeID, name)
			}
			hostPatched[name] = content
		default: // own (empty scope defaults to own per the wire contract)
			ownOut = append(ownOut, EmittedFile{
				Name:   name,
				Origin: EmitOriginPipeline,
				Body:   content,
			})
		}
	}

	res.Own = ownOut
	res.HostPatched = hostPatched
	for _, d := range walkRes.Warnings {
		res.Warnings = append(res.Warnings, fmt.Sprintf("node %q: pipeline: %s", nodeID, d.Message))
	}
	return res, nil
}

// buildPipelineRenderCtx assembles the render-time context a pipeline guest
// receives for a node: the fulfilled parameters (deterministically ordered),
// the three annotation buckets partitioned by authoring side, and the
// boundary metadata. It is the wire projection the FRP step input carries;
// the host hands it whole and the guest reads it without the host
// interpreting it.
func buildPipelineRenderCtx(node types.IRNode, bi *resolver.BoundaryInfra, parameters map[string]any) renderctx.RenderContext {
	rc := renderctx.RenderContext{
		Annotations: renderctx.RenderAnnotations{
			Aggregate: node.AggregateAnnotations,
			Release:   node.ReleaseAnnotations,
			Catalog:   node.CatalogAnnotations,
		},
		Boundary: renderctx.BoundaryContext{
			Name:     boundaryLeafName(node.BoundaryPath),
			Path:     splitBoundaryPath(node.BoundaryPath),
			SyncWave: node.SyncWave,
		},
	}
	rc.Parameters = orderedParameters(parameters)
	if bi != nil {
		rc.Boundary.Directory = bi.Directory
		rc.Boundary.SubDirectory = bi.SubDirectory
		rc.Boundary.Annotations = bi.Annotations
	}
	return rc
}

// orderedParameters projects a parameter map into a deterministically-ordered
// OrderedMap (sorted by key) so the wire payload is byte-identical across runs
// — the source map carries no author order to preserve.
func orderedParameters(params map[string]any) types.OrderedMap[any] {
	var om types.OrderedMap[any]
	if len(params) == 0 {
		return om
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		om.Set(k, params[k])
	}
	return om
}

// boundaryLeafName returns the last segment of a slash-separated boundary
// path — the boundary's authored name.
func boundaryLeafName(bp string) string {
	if bp == "" {
		return ""
	}
	if i := strings.LastIndex(bp, "/"); i >= 0 {
		return bp[i+1:]
	}
	return bp
}

// splitBoundaryPath splits a slash-separated boundary path into its segments,
// returning nil for an empty path.
func splitBoundaryPath(bp string) []string {
	if bp == "" {
		return nil
	}
	return strings.Split(bp, "/")
}

// newStepInvoker binds the pipeline's per-step guest dispatch over the shared
// plugin-resolution cascade and the describe() gate: resolve the step's plugin
// URI to its verified .wasm bytes, load them through LoadAndDescribe (which
// validates the descriptor), invoke the step's named export, and close the
// plugin. It mirrors the publishing-linter's dispatch precedent; loading per
// step is acceptable (batch amortisation is a follow-on).
func newStepInvoker(lookup pluginpath.Lookup) frp.StepInvoker {
	return func(ctx context.Context, step types.PipelineStep, payload []byte) ([]byte, error) {
		resolved, err := lookup(step.Plugin)
		if err != nil {
			return nil, err
		}
		// The external-input service rides the pipeline context (the only
		// plumbing shared with the command layer); absent, the nil field
		// yields in-band not-available envelopes for guest fetches. The loader
		// carries the process-wide shared compilation cache, so a step's guest
		// compiles once and every later step and concurrent request reuses it.
		loader := wasm.NewCachingLoaderFromContext(ctx, wasm.ExternalInputsFromContext(ctx))
		stepSpec := resolver.PluginSpec(step.Plugin, resolved)
		stepSpec.PluginType = wasm.PluginTypePipelineStep
		plugin, desc, err := loader.LoadAndDescribe(ctx, stepSpec)
		if err != nil {
			return nil, err
		}
		defer plugin.Close(ctx)

		// An export that declares a typed input/output schema types the step
		// seam: the rendered `with` input is validated against the declared
		// input before the guest runs, and the guest's returned outputs against
		// the declared output after. An absent export (undeclared function) is
		// left for InvokeExport to surface precisely; an export that declares no
		// schema is unchecked (free-form pass-through).
		export, hasExport := findExport(desc, step.Function)
		if hasExport {
			if err := validateStepInput(export, payload, step); err != nil {
				return nil, err
			}
		}
		raw, err := wasm.InvokeExport(ctx, plugin, desc, step.Function, payload)
		if err != nil {
			return nil, err
		}
		if hasExport {
			if err := validateStepOutput(export, raw, step); err != nil {
				return nil, err
			}
		}
		return raw, nil
	}
}

// validateStepInput checks the step's rendered `with` input against the
// export's declared input schema. An export that declares no input schema is a
// no-op. A value that does not satisfy the schema returns a plain,
// operator-facing error naming the step and wrapping
// [frp.ErrPipelineContractViolation] so the walk halts even for an optional
// step. The step input is taken from the marshalled [frp.FRPStepInput] payload
// the walk built, and the validator round-trips it through JSON so the schema
// sees a plain JSON value, not the ordered-map wrapper.
func validateStepInput(export wasm.PluginExport, payload []byte, step types.PipelineStep) error {
	if len(export.Input) == 0 {
		return nil
	}
	var in frp.FRPStepInput
	if err := json.Unmarshal(payload, &in); err != nil {
		return err
	}
	if err := wasm.ValidateValueAgainstSchema(export.Input, in.StepInput); err != nil {
		return fmt.Errorf("pipeline step %q `with` input does not satisfy the declared input contract: %w: %w",
			step.ID, err, frp.ErrPipelineContractViolation())
	}
	return nil
}

// validateStepOutput checks the guest's returned outputs against the export's
// declared output schema. An export that declares no output schema is a no-op.
// Outputs that do not satisfy the schema return a plain, operator-facing error
// naming the step and wrapping [frp.ErrPipelineContractViolation] so the walk
// halts even for an optional step. The outputs are taken from the returned
// [frp.FRPStepOutput] bytes, and the validator round-trips them through JSON so
// the schema sees a plain JSON value, not the ordered-map wrapper.
func validateStepOutput(export wasm.PluginExport, raw []byte, step types.PipelineStep) error {
	if len(export.Output) == 0 {
		return nil
	}
	var out frp.FRPStepOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	if err := wasm.ValidateValueAgainstSchema(export.Output, out.Outputs); err != nil {
		return fmt.Errorf("pipeline step %q returned outputs that do not satisfy the declared output contract: %w: %w",
			step.ID, err, frp.ErrPipelineContractViolation())
	}
	return nil
}

// stepDescriber reads one pipeline step's plugin describe() descriptor — the
// seam through which the executor reaches a plugin's declared providers
// and per-export capability declarations before the walk runs any guest. Reading
// the descriptor is itself a guest call, so what it precedes is the walk, not
// every crossing to a guest. The production path binds it to the shared
// resolver + describe() gate (newStepDescriber); a test supplies a canned
// descriptor so the pre-walk enforcement is exercised without a real .wasm. It
// is separate from frp.StepInvoker because enforcement needs the descriptor
// (which the invoker hides) ahead of the walk.
type stepDescriber func(ctx context.Context, step types.PipelineStep) (wasm.PluginDescriptor, error)

// newStepDescriber binds the per-step descriptor read over the shared
// plugin-resolution cascade and the describe() gate, retaining each plugin's
// self-report in descriptors so a plugin named by several steps is described
// once rather than once per step. The pre-walk compatibility check consumes the
// descriptor's compatible providers.
//
// A nil descriptors retains within the returned accessor alone rather than
// across the build: there is no unretained mode to fall into, so a caller that
// owns no build-wide retention still cannot describe one plugin twice inside a
// single pipeline.
//
// The invoker loads separately and deliberately. It executes guest code, and
// the load gate it goes through cross-checks the descriptor against the host
// functions the loaded binary actually imports — a per-instance check that a
// retained value cannot stand in for.
func newStepDescriber(descriptors *stepDescriptorMemo, lookup pluginpath.Lookup) stepDescriber {
	if descriptors == nil {
		descriptors = newStepDescriptorMemo()
	}
	return func(ctx context.Context, step types.PipelineStep) (wasm.PluginDescriptor, error) {
		return descriptors.descriptor(ctx, step.Plugin, lookup)
	}
}

// stepDescriptorMemo retains each pipeline-step plugin's validated describe()
// self-report, keyed by the plugin URI a step names, for as long as the build
// that read it runs.
//
// Retention is lazy and per plugin. The first read of a URI resolves it, loads
// the guest, describes it, closes the guest, and stores what came back; every
// later read of that URI answers from the stored value and crosses to no guest
// at all. Nothing is described ahead of a reader asking, so a plugin no step
// reaches is never loaded, and a plugin that cannot be loaded or described
// still fails at the moment its first reader asks rather than earlier.
//
// A refusal is retained beside a success, and that is the point rather than an
// economy: a plugin the host refuses is refused identically at every later
// read, so two readers of one plugin cannot disagree about whether it loads.
//
// The URI is the whole key because one build resolves a URI through one lookup,
// and that lookup's resolution cascade is fixed for the build — so a URI names
// the same verified bytes at every read of it. A reader supplies the lookup at
// each call rather than the memo holding one, which keeps the memo free of an
// opinion about where bytes come from.
//
// Reads are serialised on the mutex across the load, so two readers racing on a
// cold URI describe it once between them rather than once each.
type stepDescriptorMemo struct {
	mu      sync.Mutex
	entries map[string]stepDescriptorEntry
}

// stepDescriptorEntry is one plugin's retained outcome: the self-report it
// returned, or the error that refused it. err decides which of the two is
// meaningful.
type stepDescriptorEntry struct {
	desc wasm.PluginDescriptor
	err  error
}

// newStepDescriptorMemo returns an empty retention set, ready for its first
// read. It holds no guest and describes nothing until something asks.
func newStepDescriptorMemo() *stepDescriptorMemo {
	return &stepDescriptorMemo{entries: map[string]stepDescriptorEntry{}}
}

// descriptor returns the self-report of the plugin named by pluginURI, loading
// and describing it on the first read and answering from the retained value on
// every later one. The returned descriptor shares no mutable memory with the
// retained one, so a reader that writes through it cannot reach what a later
// reader sees.
func (m *stepDescriptorMemo) descriptor(ctx context.Context, pluginURI string, lookup pluginpath.Lookup) (wasm.PluginDescriptor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, retained := m.entries[pluginURI]
	if !retained {
		entry.desc, entry.err = loadAndDescribeStepPlugin(ctx, pluginURI, lookup)
		m.entries[pluginURI] = entry
	}
	if entry.err != nil {
		return wasm.PluginDescriptor{}, entry.err
	}
	return isolatedDescriptor(entry.desc), nil
}

// loadAndDescribeStepPlugin resolves one step plugin's URI to its verified
// .wasm bytes, loads them through LoadAndDescribe (which validates the
// descriptor), and closes the plugin before returning. The guest's lifetime
// ends inside this call; what survives it is the reported value alone.
//
// It resolves and loads exactly once per call, with no other path to either, so
// the number of times it runs is the number of times the build crosses to a
// step guest to ask what it is.
func loadAndDescribeStepPlugin(ctx context.Context, pluginURI string, lookup pluginpath.Lookup) (wasm.PluginDescriptor, error) {
	resolved, err := lookup(pluginURI)
	if err != nil {
		return wasm.PluginDescriptor{}, err
	}
	// Same context-carried service as newStepInvoker: describe() runs
	// guest code too, so its loader carries the same fetch surface and the
	// same process-wide shared compilation cache.
	loader := wasm.NewCachingLoaderFromContext(ctx, wasm.ExternalInputsFromContext(ctx))
	stepSpec := resolver.PluginSpec(pluginURI, resolved)
	stepSpec.PluginType = wasm.PluginTypePipelineStep
	plugin, desc, err := loader.LoadAndDescribe(ctx, stepSpec)
	if err != nil {
		return wasm.PluginDescriptor{}, err
	}
	_ = plugin.Close(ctx)
	return desc, nil
}

// isolatedDescriptor returns a copy of desc sharing no mutable memory with it,
// so a reader that writes through a returned slice, pointer or raw message
// cannot reach the retained value every later reader answers from. Returning
// the value plainly would copy the struct and none of what its fields point at,
// and the descriptor carries slices, pointers and raw JSON messages that would
// each go on aliasing.
//
// The copy is derived from the value's own structure rather than written field
// by field, because the descriptor is generated from the plugin self-report
// contract and gains a field whenever that contract does. A hand-written copy
// would keep compiling against the wider shape while silently sharing whatever
// was added — a divergence that shows in no diff and fails no test.
func isolatedDescriptor(desc wasm.PluginDescriptor) wasm.PluginDescriptor {
	return isolate(reflect.ValueOf(desc)).Interface().(wasm.PluginDescriptor)
}

// isolate returns a value of v's type that shares no mutable memory with v,
// recursing through every kind that can alias: a pointer's target, a slice's
// and an array's elements, a map's keys and values, an interface's dynamic
// value, and a struct's fields. A nil pointer, slice or map is returned as it
// is, so the distinction between an absent value and an empty one survives the
// copy — the descriptor's optional fields are read for presence, not only for
// content.
//
// A field the reflect package cannot set is left as the enclosing struct copy
// put it, which for an unexported field means it would still alias. That is not
// a silent gap: the descriptor's field tree is asserted elsewhere to hold no
// such field, so one appearing fails a test rather than quietly weakening the
// isolation.
func isolate(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(isolate(v.Elem()))
		return out
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(isolate(v.Index(i)))
		}
		return out
	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(isolate(v.Index(i)))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(isolate(iter.Key()), isolate(iter.Value()))
		}
		return out
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(isolate(v.Elem()))
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := 0; i < v.NumField(); i++ {
			f := out.Field(i)
			if !f.CanSet() {
				continue
			}
			f.Set(isolate(v.Field(i)))
		}
		return out
	default:
		return v
	}
}

// enforceProviderCompatibility runs the pre-walk provider-compatibility check
// over every pipeline step. For each step it reads the plugin descriptor, finds
// the step's export by name, and asserts the provider rendering every scope the
// export declares is in the plugin's compatible providers (exact-string,
// enumerated — no family abstraction). An own-scope export checks ownProvider;
// a host-scope export checks hostProvider, but only when a host is present
// (hostProvider != ""): a host operation from a hostless step is already caught
// by ErrPipelineHostScopeUnavailable at the operation level, so the
// compatibility check does not double-report it. An empty compatible-providers
// set declares no compatibility claim and therefore matches no provider, so any
// step that uses such a plugin halts. The check is fail-fast: the first
// mismatch halts the build before the walk runs any guest.
//
// An own-scope step with an empty ownProvider is the post-assembly replay of a
// deferred host-only record — a component-scope FeatureRecipe whose host is an
// assembler defers its whole pipeline but records no own provider, because the
// FeatureRecipe's own output is never assembled. That step has no merged own
// document to patch after assembly; it halts via pipelineOwnOnAssemblerErr with
// a cause-naming message rather than reporting an empty-provider compatibility
// claim.
func enforceProviderCompatibility(
	ctx context.Context,
	steps []types.PipelineStep,
	ownProvider string,
	hostProvider string,
	describe stepDescriber,
	nodeID string,
) error {
	for _, step := range steps {
		desc, err := describe(ctx, step)
		if err != nil {
			return wrapPipelineErr(err, nodeID)
		}
		scope := exportScope(desc, step.Function)
		switch scope {
		case wasm.ScopeHost:
			// Only enforce host compatibility when a host is present; a hostless
			// /host op is the hostless guardrail's territory, not this check's.
			if hostProvider != "" && !providerCompatible(hostProvider, desc.Capabilities) {
				return pipelineProviderIncompatibleErr(nodeID, step.Plugin, desc.Capabilities, "host", hostProvider)
			}
		default: // own (empty scope defaults to own per the wire contract)
			if ownProvider == "" {
				// A deferred host-only record (a component-scope FeatureRecipe
				// whose host is an assembler) carries no own provider: the FR's
				// own output is emitted as discrete fragment files and is never
				// assembled, so a deferred own-scope step has no merged own
				// document to replay against. The empty ownProvider is the
				// signature of that case here (a per-node own pipeline always
				// carries its spec.capability). Halt with a cause-naming message
				// rather than the bare provider-incompatibility report, whose
				// empty provider name reads as a malformed claim. The replay
				// wrap names the FeatureRecipe instance and node around this.
				return pipelineOwnOnAssemblerErr(nodeID, step.ID)
			}
			if !providerCompatible(ownProvider, desc.Capabilities) {
				return pipelineProviderIncompatibleErr(nodeID, step.Plugin, desc.Capabilities, "own", ownProvider)
			}
		}
	}
	return nil
}

// findExport returns the descriptor's export whose name matches function, and
// whether one was found. It is the single find-an-export-by-name home the
// pipeline seam shares: the compatibility check reads the found export's scope,
// and the invoker reads its declared input/output schemas.
func findExport(desc wasm.PluginDescriptor, function string) (wasm.PluginExport, bool) {
	for _, e := range desc.Exports {
		if e.Name == function {
			return e, true
		}
	}
	return wasm.PluginExport{}, false
}

// exportScope returns the declared scope of the named export, or the empty
// string when the descriptor declares no such export (the walk's InvokeExport
// gate surfaces an undeclared export precisely; here an absent export yields the
// own-scope default so a malformed descriptor is not silently treated as host).
func exportScope(desc wasm.PluginDescriptor, function string) string {
	if e, ok := findExport(desc, function); ok {
		return e.Scope
	}
	return ""
}

// providerCompatible reports whether provider is an exact-string member of the
// plugin's declared providers. An empty set matches nothing, so it
// returns false for every provider — an empty list declares no compatibility
// claim and the caller halts.
func providerCompatible(provider string, compatible []string) bool {
	for _, c := range compatible {
		if c == provider {
			return true
		}
	}
	return false
}

// buildScopeDocs carves each emitted file into addressable units via the
// codec and keys each by its opaque positional token into an
// OrderedMap[Unit], the scope root the walk operates on. A structured unit
// carries a Tree (the guest's PATCH target); an opaque unit carries only its
// media type and bytes (a SELECT/EMIT target). The file's units are retained
// on the pipelineFile so fold-back can reassemble a patched file
// byte-faithfully through the codec.
//
// A file the codec rejects entirely (no addressable units) is recorded with
// nil units and contributes nothing to the set — it can only ever be an EMIT
// sibling, never a PATCH target.
func buildScopeDocs(codec frp.Codec, files []EmittedFile) ([]pipelineFile, types.OrderedMap[frp.Unit], error) {
	out := make([]pipelineFile, 0, len(files))
	var docs types.OrderedMap[frp.Unit]
	for _, f := range files {
		pf := pipelineFile{file: pipelineFile0{Name: f.Name, Body: f.Body, Origin: f.Origin, Intermediate: f.Intermediate}}
		units, err := codec.Segment(f.Name, f.Body)
		if err != nil {
			// The codec produced no addressable units: retain verbatim. Only
			// an actual PATCH addressing a (non-existent) token halts later.
			pf.units = nil
			out = append(out, pf)
			continue
		}
		pf.units = units
		for _, u := range units {
			docs.Set(u.Token, u)
		}
		out = append(out, pf)
	}
	return out, docs, nil
}

// foldPatchesBack rebuilds each file through the codec, splicing back only the
// units a step actually patched (those carrying applied ops). For a file with
// patched units the codec edits the addressed byte-spans surgically and
// preserves every untouched byte; a file with no patched unit passes through
// verbatim. A PATCH that addressed an opaque (non-structured) token surfaces
// as the renderer's not-structured Loud Guardrail.
func foldPatchesBack(
	codec frp.Codec,
	pfs []pipelineFile,
	appliedByToken map[string][]frp.JSONPatchOp,
	nodeID string,
) ([]EmittedFile, []string, error) {
	out := make([]EmittedFile, 0, len(pfs))
	var warnings []string
	for _, pf := range pfs {
		var patched []frp.PatchedUnit
		for _, u := range pf.units {
			ops, ok := appliedByToken[u.Token]
			if !ok || len(ops) == 0 {
				continue
			}
			if !u.Structured() {
				return nil, nil, pipelineNotStructuredErr(nodeID, u.Token)
			}
			patched = append(patched, frp.PatchedUnit{Token: u.Token, Tree: u.Tree, Ops: ops})
		}
		body := pf.file.Body
		if len(patched) > 0 {
			b, warns, err := codec.Reassemble(pf.file.Name, pf.file.Body, pf.units, patched)
			if err != nil {
				return nil, nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
					fmt.Sprintf("node %q: pipeline: reassemble patched file %q", nodeID, pf.file.Name))
			}
			body = b
			for _, w := range warns {
				warnings = append(warnings, fmt.Sprintf("node %q: pipeline: %s", nodeID, w))
			}
		}
		out = append(out, EmittedFile{Name: pf.file.Name, Origin: pf.file.Origin, Body: body, Intermediate: pf.file.Intermediate})
	}
	return out, warnings, nil
}

// changedFragmentOwn reports the first own-scope token a step patched that
// belongs to an Intermediate (pre-assembly fragment) file. A PATCH against
// such a fragment halts: the assembler has not produced discrete documents
// yet. A patched token is one carrying applied ops, so an EMIT (a new token,
// no ops) never trips this guard.
func changedFragmentOwn(appliedByToken map[string][]frp.JSONPatchOp, pfs []pipelineFile) (string, bool) {
	for _, pf := range pfs {
		if !pf.file.Intermediate {
			continue
		}
		for _, u := range pf.units {
			if ops, ok := appliedByToken[u.Token]; ok && len(ops) > 0 {
				return u.Token, true
			}
		}
	}
	return "", false
}

// firstChangedToken returns the lowest-sorted token a step patched in the
// given applied-ops map. A new token an EMIT produced carries no applied ops,
// so it is never reported here — only a PATCH trips this guard. Token order is
// fixed (sorted) so the halt names the same token across runs.
func firstChangedToken(appliedByToken map[string][]frp.JSONPatchOp) (string, bool) {
	keys := make([]string, 0, len(appliedByToken))
	for k, ops := range appliedByToken {
		if len(ops) > 0 {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return "", false
	}
	sort.Strings(keys)
	return keys[0], true
}

// lookupFile returns the pipelineFile for a given emitted-file name.
func lookupFile(pfs []pipelineFile, name string) (pipelineFile, bool) {
	for _, pf := range pfs {
		if pf.file.Name == name {
			return pf, true
		}
	}
	return pipelineFile{}, false
}

// sideEffectBytes extracts the bytes an EMIT side effect contributes: the file
// content for emit_file, the CRD document for emit_crd. An unrecognised kind
// halts loudly rather than silently dropping.
func sideEffectBytes(se frp.SideEffect, nodeID string, idx int) ([]byte, error) {
	switch se.Kind {
	case "emit_file":
		if se.File == nil {
			return nil, cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
				fmt.Sprintf("node %q: pipeline: emit_file side effect %d has no file", nodeID, idx), nil, nil)
		}
		return []byte(se.File.Content), nil
	case "emit_crd":
		return se.CRD, nil
	default:
		return nil, cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
			fmt.Sprintf("node %q: pipeline: unknown side-effect kind %q", nodeID, se.Kind), nil, nil)
	}
}

// sideEffectName returns the emitted-file path an EMIT contributes: the
// declared file path for emit_file, a deterministic derived name for emit_crd.
func sideEffectName(se frp.SideEffect, idx int) string {
	if se.Kind == "emit_file" && se.File != nil && se.File.Path != "" {
		return se.File.Path
	}
	return fmt.Sprintf("pipeline-emit-%d.yaml", idx)
}

// mapWalkError promotes the pipeline walk's local error causes to the
// renderer's build-halting Loud Guardrails. An absent patch target becomes
// ErrPipelineTargetNotEmitted; a PATCH against an opaque (non-structured)
// unit becomes ErrPipelinePatchTargetNotStructured; a /host operation from a
// hostless step becomes ErrPipelineHostScopeUnavailable; any other walk
// failure is wrapped as a catalog error naming the node.
func mapWalkError(err error, nodeID string, hasHost bool) error {
	if errors.Is(err, frp.ErrPipelineTargetOpaque()) {
		// The codec declined a tree-view for the addressed unit; the guest
		// addressed it with a PATCH anyway. Chain the walk's cause so both
		// sentinels stay matchable via errors.Is.
		inner := fmt.Errorf("%w: %w", ErrPipelinePatchTargetNotStructured(), err)
		return cerrors.Wrap(inner, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
			fmt.Sprintf("node %q: %s", nodeID, ErrPipelinePatchTargetNotStructured().Error()))
	}
	if errors.Is(err, frp.ErrPipelineTargetAbsent()) {
		// A boundary-scope / hostless step that addressed /host surfaces here as
		// an absent target on the empty Host root; report the precise host-scope
		// diagnostic when the step had no host component. Chain the walk's cause
		// (two %w verbs) so both the renderer sentinel and the walk's
		// ErrPipelineTargetAbsent stay matchable via errors.Is.
		if !hasHost && mentionsHostScope(err) {
			inner := fmt.Errorf("%w: %w", ErrPipelineHostScopeUnavailable(), err)
			return cerrors.Wrap(inner, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
				fmt.Sprintf("node %q: %s", nodeID, ErrPipelineHostScopeUnavailable().Error()))
		}
		inner := fmt.Errorf("%w: %w", ErrPipelineTargetNotEmitted(), err)
		return cerrors.Wrap(inner, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
			fmt.Sprintf("node %q: %s", nodeID, ErrPipelineTargetNotEmitted().Error()))
	}
	return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
		fmt.Sprintf("node %q: pipeline step failed", nodeID))
}

// mentionsHostScope reports whether an absent-target error addressed the host
// scope. The walk wraps the offending scope into the message; matching on it
// distinguishes a /host op from a hostless step from a genuine own-scope
// absent target.
func mentionsHostScope(err error) bool {
	return err != nil && strings.Contains(err.Error(), `scope "host"`)
}

func wrapPipelineErr(err error, nodeID string) error {
	return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
		fmt.Sprintf("node %q: pipeline document set construction", nodeID))
}

func pipelineNotStructuredErr(nodeID, token string) error {
	return cerrors.Wrap(ErrPipelinePatchTargetNotStructured(), cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
		fmt.Sprintf("node %q: %s: document %q", nodeID, ErrPipelinePatchTargetNotStructured().Error(), token))
}

func pipelinePreAssemblyErr(nodeID, token, scope string) error {
	return cerrors.Wrap(ErrPipelinePatchPreAssembly(), cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
		fmt.Sprintf("node %q: %s (%s-scope document %q is rendered by an assembler provider; the post-assembly phase re-runs this PATCH once the documents are discrete)",
			nodeID, ErrPipelinePatchPreAssembly().Error(), scope, token))
}

// pipelineOwnOnAssemblerErr is the Loud Guardrail for a deferred own-scope
// pipeline step that has no merged own document to run against: a
// component-scope FeatureRecipe whose host is an assembler defers its whole
// pipeline to the post-assembly phase, but the FeatureRecipe's own output is
// emitted as discrete fragment files and is never assembled, so the deferred
// record carries no own provider and an own-scope step has nothing to patch
// after assembly. It reuses ErrPipelinePatchPreAssembly (the own target would
// be an unassembled assembler fragment) and names the real cause plus the
// remedies, instead of the bare provider-incompatibility report whose empty
// provider name reads as a malformed compatibility claim.
func pipelineOwnOnAssemblerErr(nodeID, stepID string) error {
	return cerrors.Wrap(ErrPipelinePatchPreAssembly(), cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
		fmt.Sprintf("node %q: own-scope pipeline step %q cannot run on an assembler provider. A component-scope FeatureRecipe's own output is emitted as discrete fragment files and is never assembled, so an own-scope step has no merged document to patch after assembly. Remedy: target a non-assembler provider for the FeatureRecipe's own output, or move the step to /host, or remove it",
			nodeID, stepID))
}

// pipelineProviderIncompatibleErr builds the Loud Guardrail for a step whose
// plugin is not compatible with the provider rendering the scope it touches. The
// message names the plugin, the offending provider and its scope, and the
// plugin's declared providers in declared order (the descriptor slice
// is already author/declared order, so the message is byte-identical across
// runs); an empty declared set renders as "(none)" so the no-claim case reads
// clearly.
func pipelineProviderIncompatibleErr(nodeID, plugin string, declared []string, scope, provider string) error {
	declaredList := "(none)"
	if len(declared) > 0 {
		declaredList = strings.Join(declared, ", ")
	}
	return cerrors.Wrap(ErrPluginProviderIncompatible(), cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
		fmt.Sprintf("node %q: %s: plugin %q does not list provider %q (the %s-scope provider); declared targets: [%s]",
			nodeID, ErrPluginProviderIncompatible().Error(), plugin, provider, scope, declaredList))
}

func pipelineHostScopeErr(nodeID, name string) error {
	return cerrors.Wrap(ErrPipelineHostScopeUnavailable(), cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
		fmt.Sprintf("node %q: %s (emit %q)", nodeID, ErrPipelineHostScopeUnavailable().Error(), name))
}
