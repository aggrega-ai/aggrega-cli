// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1

package terraform

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// HostCaps returns terraform's host-side capability holder, built from the
// provider package's standalone functions rather than methods on a concrete
// engine — so the host can resolve terraform's host capabilities independently of
// any engine identity, including when a node renders through a Wasm guest.
//
// terraform holds no host-side capabilities: it is a flat-output provider that
// merges nothing at the dispatch boundary, resolves and vendors its module source
// itself from the host's name-only listing, and places its artifacts itself in the
// guest (prepending build provenance as an HCL comment for a pure-HCL body, or
// emitting a companion provenance file when a .tf.json body is present). The input
// kinds, boundary-assembly mode, and artifact-template delimiters are all the
// guest's own describe() self-report.
func HostCaps() renderer.HostCapabilities {
	return renderer.HostCapabilities{}
}
