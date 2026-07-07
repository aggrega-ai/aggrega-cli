// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1

package helm

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// HostCaps returns helm's host-side capability holder, built from the provider
// package's standalone functions rather than methods on a concrete engine — so
// the host can resolve helm's host capabilities independently of any engine
// identity, including when a node renders through a Wasm guest. The input kinds
// and boundary-assembly mode are not held here; they are the guest's own
// describe() self-report. helm holds no host-side capabilities: it resolves and
// packs its chart source itself from the host's name-only source listing, and
// declares its codec, input kinds, and assembly through describe().
func HostCaps() renderer.HostCapabilities {
	return renderer.HostCapabilities{}
}
