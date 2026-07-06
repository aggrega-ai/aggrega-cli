// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1

package compose

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// hostCapabilities reports compose's input-kind support: sourced (a compose
// file) and synthesised (Workload → service fragment). compose does NOT render
// artifacts-only: as a boundary assembler it merges per-node output into
// docker-compose.yaml services, and an artifacts-only node has none to
// contribute. The describe() analog for this provider, and the single home the
// standalone HostCaps holder reads.
func hostCapabilities() renderer.ProviderCapabilities {
	return renderer.ProviderCapabilities{Sourced: true, Synthesized: true}
}

// HostCaps returns compose's host-side capability holder, built from the
// provider package's standalone functions rather than methods on a concrete
// engine — so the host can resolve compose's host capabilities independently of
// any engine identity, including when a node renders through a Wasm guest.
func HostCaps() renderer.HostCapabilities {
	return renderer.HostCapabilities{
		Caps:                         hostCapabilities(),
		ProjectSource:                ProjectComposeSource,
		AssemblesBoundary:            true,
		BoundaryConsumerEnvOverrides: computeBridgeConsumerEnvOverrides,
	}
}
