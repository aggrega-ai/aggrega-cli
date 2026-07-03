// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1

// Host adapter for Workload → Docker Compose service-fragment synthesis.
// Implements the renderer.WorkloadSynthesizer interface (declared in
// renderer/synthesis.go, dispatched from Phase B.4). The synthesis itself lives
// in the tag-free SynthesizeWorkloadCore (synth_core.go), which both this host
// adapter and the provider Wasm guest call so the two synthesis surfaces stay
// byte-for-byte identical. Compose's synthesis consumes no per-slot secret
// provides, so this adapter forwards only the evaluated Workload and the node
// context.

package compose

import (
	"context"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// SynthesizeWorkload translates a parsed Workload into a per-node compose
// fragment. Implements renderer.WorkloadSynthesizer. Returns one EmittedFile
// (`<nodeID>.compose-fragment.yaml`); the Track 2 merger consumes fragments
// to produce the Release's single docker-compose.yaml.
//
// The dispatcher (renderer/synthesis.go) tags the EmittedFile's Origin =
// EmitOriginWorkload before appending to PhaseBState — the synthesizer
// itself doesn't set Origin.
func (e *ComposeEngine) SynthesizeWorkload(
	_ context.Context,
	_ *types.TemplateContext,
	w *types.Workload,
	nodeCtx renderer.NodeRenderContext,
) ([]renderer.EmittedFile, error) {
	if w == nil {
		return nil, nil
	}
	return SynthesizeWorkloadCore(w, nodeCtx)
}
