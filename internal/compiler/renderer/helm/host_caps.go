// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1

package helm

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// hostCapabilities reports helm's input-kind support: sourced (chart source),
// synthesised (Workload), and artifacts-only (flat per-node manifest emission).
// The describe() analog for this provider, and the single home the standalone
// HostCaps holder reads.
func hostCapabilities() renderer.ProviderCapabilities {
	return renderer.ProviderCapabilities{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
}

// HostCaps returns helm's host-side capability holder, built from the provider
// package's standalone functions rather than methods on a concrete engine — so
// the host can resolve helm's host capabilities independently of any engine
// identity, including when a node renders through a Wasm guest.
func HostCaps() renderer.HostCapabilities {
	return renderer.HostCapabilities{
		Caps:          hostCapabilities(),
		ProjectSource: ProjectChartSource,
	}
}
