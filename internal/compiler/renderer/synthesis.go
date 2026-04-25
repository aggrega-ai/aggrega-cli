// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"fmt"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// WorkloadSynthesizer is the optional interface a provider engine implements
// to translate a parsed types.Workload into provider-native manifests. Phase
// B.4 type-asserts the engine to this interface when a Recipe has declared
// workload: or workloadFrom:; engines that don't implement it produce a
// CATALOG_ERROR at B.4.
//
// Implemented by: helm engine (lands in WI05 of synthesis-foundations);
// compose engine (lands in WI06). Other providers (terraform,
// cloudformation, eval) do not implement WorkloadSynthesizer today.
type WorkloadSynthesizer interface {
	// SynthesizeWorkload produces provider-native manifests from a parsed
	// Workload. ctx is the per-node TemplateContext (gives access to
	// .context.instance_id, .values, .infra, etc.); w is the
	// CUE-validated, secretsSchema-checked Workload; nodeCtx carries the
	// boundary path / labels / annotations.
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
		return nil, fmt.Errorf(
			"CATALOG_ERROR: provider %q does not implement WorkloadSynthesizer; the Recipe declares workload: or workloadFrom: but the bound provider has no synthesised-mode implementation",
			providerName,
		)
	}
	files, err := synth.SynthesizeWorkload(ctx, tmplCtx, w, nodeCtx)
	if err != nil {
		return nil, fmt.Errorf("workload synthesis: %w", err)
	}
	// Tag every emitted file with the workload origin; synthesizers don't
	// set Origin themselves so this stays a single load-bearing call site.
	for i := range files {
		files[i].Origin = EmitOriginWorkload
	}
	return files, nil
}
