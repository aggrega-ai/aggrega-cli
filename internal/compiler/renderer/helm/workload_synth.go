// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1

// Host adapter for Workload → K8s manifest synthesis. Implements the
// renderer.WorkloadSynthesizer interface (declared in renderer/synthesis.go,
// dispatched from Phase B.4). The synthesis itself lives in the tag-free
// SynthesizeWorkloadCore (synth_core.go), which both this host adapter and the
// provider Wasm guest call so the two synthesis surfaces stay byte-for-byte
// identical. This adapter's only job is to bridge the host-only template
// context into the core's plain secret-provides map.

package helm

import (
	"context"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// SynthesizeWorkload translates a parsed Workload into K8s manifest files for
// the helm provider. Implements renderer.WorkloadSynthesizer; called by
// Phase B.4 when the bound Recipe declares workload:.
//
// It projects the host-only template context into the plain per-slot secret
// provides map the tag-free SynthesizeWorkloadCore consumes, then delegates.
// Returns []EmittedFile{Name, Body}; the dispatcher (renderer/synthesis.go)
// tags Origin = EmitOriginWorkload before appending to PhaseBState.
func (e *HelmEngine) SynthesizeWorkload(
	_ context.Context,
	tmplCtx *types.TemplateContext,
	w *types.Workload,
	nodeCtx renderer.NodeRenderContext,
) ([]renderer.EmittedFile, error) {
	if w == nil {
		return nil, nil
	}
	return SynthesizeWorkloadCore(w, SecretProvidesFromContext(tmplCtx), nodeCtx)
}

// SecretProvidesFromContext projects each secret slot's resolved provides into
// a slot→provides lookup the synthesizer reads to resolve a workload secret
// reference to the generated Secret's identity. A workload secrets entry's
// `from` is the slot; the generated Secret's identity (name, key) comes from the
// slot's provides — resolved by the orchestrator before Phase B and exposed on
// the template context. The orchestrator populates the per-slot provides before
// Phase B; a nil context yields an empty lookup.
//
// Exported so a host-side renderer engine that projects a workload onto the
// provider render wire builds the per-slot secret map from the same source of
// truth this in-tree synthesizer reads, rather than re-deriving it.
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
