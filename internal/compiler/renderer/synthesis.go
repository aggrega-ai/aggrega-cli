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
// to translate a parsed types.Workload into provider-native manifests. The
// synthesis dispatch (phaseB4, through dispatchWorkloadSynthesis) type-asserts
// the engine to this interface when a Recipe has declared workload:, and an
// engine that does not implement it produces a CATALOG_ERROR there.
//
// That assertion is not the check deciding whether a provider may synthesise.
// Which input kinds a provider may be handed is settled once per node, before any
// render dispatch, by the central input-kind gate reading the provider's own
// declared [ProviderContract]; a provider that does not claim synthesised input is
// refused there. The assertion cannot refuse a provider this host ships at all,
// because the host renders through a single provider adapter which satisfies this
// interface by compile-time assertion whatever its guest declares.
//
// Synthesis starts from a declared Workload and from nothing else — which is
// why the parameter is a *types.Workload rather than the node's values. A
// provider never reads a values dictionary and decides for itself that the
// author must have wanted a running service: compute exists in the output
// because the developer declared it, or it does not exist. A Recipe that
// declares neither source: nor workload: is an artifacts-only Recipe, rendered
// by a provider that reports ArtifactsStandalone and emitting exactly the
// artifacts it declared; it never becomes a silently synthesized compute unit.
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

// dispatchWorkloadSynthesis is the synthesis dispatch's nil-safe entry point: a
// nil Workload is not a fault and yields no files, so a node that declares no
// workload: passes straight through. Exposed for the orchestrator and for unit
// tests.
//
// A non-nil Workload reaching an engine that does not implement
// WorkloadSynthesizer returns a CATALOG_ERROR-shaped error naming the provider.
// That branch guards a state neither render path produces: the input-kind gate has
// already refused any provider whose declared contract does not claim synthesised
// input, and the single provider adapter the host renders through implements the
// interface by compile-time assertion. It is kept loud rather than reduced to an
// empty file set because a dispatch emitting nothing for a declared workload would
// drop compute the author asked for and report success.
func dispatchWorkloadSynthesis(
	ctx context.Context,
	engine Provider,
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
			"provider %q has no synthesised-mode implementation; the Recipe declares a workload: block, which needs one",
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
// renderer so every host-side surface that reads it — the workload-reference
// validation and the wasmengine adapter that projects a workload onto the
// provider render wire — builds the lookup from one source of truth rather than
// re-deriving it.
//
// Sensitivity is deliberately absent from this projection. Whether an attachment
// handles declared-sensitive material is already on the node projection's feature
// entry for the same attachment, under the same instance name, so a second copy
// keyed the same way would be one derived fact carried twice on one payload.
func AttachmentProvidesFromContext(tmplCtx *types.TemplateContext) map[string]types.OrderedMap[string] {
	provides := map[string]types.OrderedMap[string]{}
	if tmplCtx == nil {
		return provides
	}
	for name, attachment := range tmplCtx.Features.All() {
		provides[name] = attachment.Provides
	}
	return provides
}
