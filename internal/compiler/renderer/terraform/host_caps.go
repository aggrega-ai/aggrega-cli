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
// terraform is a flat-output provider (it merges nothing at the dispatch
// boundary) that handles its artifacts through the external path (ExternalArtifacts
// true) and emits those artifacts verbatim (VerbatimArtifacts true) — a terraform
// config body (HCL or .tf.json) is not Kubernetes YAML, so it emits byte-for-byte
// after Go-template evaluation rather than being parsed as YAML and stamped with
// provenance labels. ProjectSource walks the acquired module directory and packs
// its files onto the render wire, so the guest reconstructs and vendors the module
// in memory without filesystem access. The input kinds and boundary-assembly mode
// are not held here; they are the guest's own describe() self-report.
func HostCaps() renderer.HostCapabilities {
	return renderer.HostCapabilities{
		ExternalArtifacts: true,
		VerbatimArtifacts: true,
		ProjectSource:     ProjectTerraformSource,
	}
}
