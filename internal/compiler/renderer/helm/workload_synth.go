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
	return SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(tmplCtx), nodeCtx)
}
