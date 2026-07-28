// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"fmt"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// WorkloadSynthesizer is the optional interface a provider engine implements
// to translate a parsed types.Workload into provider-native manifests. Phase
// B.4 type-asserts the engine to this interface when a Recipe has declared
// workload:; engines that don't implement it produce a CATALOG_ERROR at B.4.
//
// Implemented by: helm and compose engines. Other providers (terraform,
// cloudformation, eval) do not implement WorkloadSynthesizer today.
type WorkloadSynthesizer interface {
	// SynthesizeWorkload produces provider-native manifests from a parsed
	// Workload. ctx is the per-node TemplateContext (gives access to
	// .context.instance_id, .values, .infra, etc.); w is the
	// schema-validated, featuresSchema-checked Workload; nodeCtx carries
	// the boundary path / labels / annotations.
	//
	// Returns []EmittedFile (each tagged EmitOriginWorkload by the caller
	// at Phase B.4 — the synthesizer itself doesn't set Origin) or a
	// translation error.
	SynthesizeWorkload(ctx context.Context, tmplCtx *types.TemplateContext, w *types.Workload, nodeCtx NodeRenderContext) ([]EmittedFile, error)
}

// dispatchWorkloadSynthesis is Phase B.4's nil-safe entry point: returns
// []EmittedFile{} when the engine doesn't implement WorkloadSynthesizer or
// when w is nil. Exposed for the orchestrator and for unit tests.
//
// When workload synthesis is required (w != nil) but the engine doesn't
// implement WorkloadSynthesizer, returns a CATALOG_ERROR-shaped error
// naming the provider that lacks the capability.
func dispatchWorkloadSynthesis(
	ctx context.Context,
	engine Engine,
	tmplCtx *types.TemplateContext,
	w *types.Workload,
	nodeCtx NodeRenderContext,
	providerName string,
) ([]EmittedFile, error) {
	if w == nil {
		return nil, nil
	}
	synth, ok := engine.(WorkloadSynthesizer)
	if !ok {
		inner := fmt.Errorf(
			"CATALOG_ERROR: provider %q does not implement WorkloadSynthesizer; the Recipe declares workload: but the bound provider has no synthesised-mode implementation",
			providerName,
		)
		return nil, cerrors.Wrap(inner, cerrors.CatalogError, cerrors.SourceLocation{}, inner.Error())
	}
	files, err := synth.SynthesizeWorkload(ctx, tmplCtx, w, nodeCtx)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, "workload synthesis")
	}
	// Tag every emitted file with the workload origin; synthesizers don't
	// set Origin themselves so this stays a single load-bearing call site.
	for i := range files {
		files[i].Origin = EmitOriginWorkload
	}
	return files, nil
}

// AttachmentProvidesFromContext projects each attachment's resolved provides
// into an instance-name→provides lookup a WorkloadSynthesizer reads to resolve a
// workload attachment reference to the emitted resource's identity. A workload
// attachments entry's `from` names the slot; the resource's identity (name, key)
// comes from the provides of whatever fills that slot — resolved by the
// orchestrator before Phase B and exposed on the template context. A nil context
// yields an empty lookup.
//
// This is a render-context projection, not provider logic: it lives in the
// renderer so every host-side synthesizer surface — the in-tree provider
// engines and the wasmengine adapter that projects a workload onto the provider
// render wire — builds the lookup from one source of truth rather than
// re-deriving it.
func AttachmentProvidesFromContext(tmplCtx *types.TemplateContext) map[string]types.OrderedMap[string] {
	provides, _ := AttachmentProjectionFromContext(tmplCtx, types.OrderedMap[types.IRFeatureEntry]{})
	return provides
}

// AttachmentProjectionFromContext projects the node's attachments into the two
// per-attachment lookups a workload projection carries: each attachment's
// resolved provides, and whether it handles material an author declared
// sensitive. The sensitivity value is the marking the IR attachment entry already
// carries — derived once at IR-build time from the Feature's and the slot's
// declarations — so nothing is derived a second time here; attachments supplies
// those entries, keyed by the same instance names the template context uses.
//
// Both maps come out of ONE walk over ONE key set, which is what keeps them from
// drifting apart: a second walk could produce a sensitivity entry for an
// attachment the provides map has no key for, and the two maps are read together
// by whatever resolves an attachment reference. Only a marked attachment gets a
// sensitivity entry, so an attachment absent from the second map is not declared
// sensitive and a node with no marked attachment yields an empty map that elides
// from the wire.
//
// A nil context yields two empty lookups. An empty attachments map yields the
// provides lookup alone, which is how the provides-only callers reach this.
func AttachmentProjectionFromContext(
	tmplCtx *types.TemplateContext,
	attachments types.OrderedMap[types.IRFeatureEntry],
) (map[string]types.OrderedMap[string], map[string]bool) {
	provides := map[string]types.OrderedMap[string]{}
	sensitive := map[string]bool{}
	if tmplCtx == nil {
		return provides, sensitive
	}
	for name, attachment := range tmplCtx.Features.All() {
		provides[name] = attachment.Provides
		if entry, ok := attachments.Get(name); ok && entry.Sensitive {
			sensitive[name] = true
		}
	}
	return provides, sensitive
}
