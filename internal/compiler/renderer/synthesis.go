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

// SecretProvidesFromContext projects each secret slot's resolved provides into
// a slot→provides lookup a WorkloadSynthesizer reads to resolve a workload
// secret reference to the generated Secret's identity. A workload secrets
// entry's `from` is the slot; the generated Secret's identity (name, key) comes
// from the slot's provides — resolved by the orchestrator before Phase B and
// exposed on the template context. The orchestrator populates the per-slot
// provides before Phase B; a nil context yields an empty lookup.
//
// This is a render-context projection, not provider logic: it lives in the
// renderer so every host-side synthesizer surface — the in-tree provider
// engines and the wasmengine adapter that projects a workload onto the provider
// render wire — builds the per-slot secret map from one source of truth rather
// than re-deriving it.
func SecretProvidesFromContext(tmplCtx *types.TemplateContext) map[string]types.OrderedMap[string] {
	out := map[string]types.OrderedMap[string]{}
	if tmplCtx == nil {
		return out
	}
	for slot, sec := range tmplCtx.Secrets.All() {
		out[slot] = sec.Provides
	}
	return out
}
