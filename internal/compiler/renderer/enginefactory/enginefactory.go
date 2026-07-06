// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package enginefactory provides the canonical provider-name -> host-capability
// resolution for the compiler's render routing. It is the single source of truth
// for which provider names the compiler renders and the host-side capability
// holder each maps to; both the CLI build and the build-faithful test harness
// consume it, so a test resolves provider capabilities exactly as a real build
// does.
package enginefactory

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	renderCompose "github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/compose"
	renderHelm "github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helm"
	renderHelmChart "github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helmchart"
	renderTerraform "github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/terraform"
)

// HostCapsFor returns a provider's host-side capability holder by name, resolved
// from the provider package's own holder function — so the Wasm render route
// reads one source of a provider's host capabilities and cannot drift. It
// obtains the holder without constructing any engine, so the render route (which
// takes a holder, not an engine) never depends on a constructed engine instance.
// The empty-string branch is a defensive default to "helm"; the Recipe schema
// requires spec.provider, so empty values do not reach here in normal flow. ok
// is false for a provider name that binds to no shipped provider.
func HostCapsFor(providerName string) (renderer.HostCapabilities, bool) {
	switch providerName {
	case "helm", "":
		return renderHelm.HostCaps(), true
	case "helm-chart":
		return renderHelmChart.HostCaps(), true
	case "compose":
		return renderCompose.HostCaps(), true
	case "terraform":
		return renderTerraform.HostCaps(), true
	default:
		return renderer.HostCapabilities{}, false
	}
}

// CapabilitiesFor reports a provider's input-kind capabilities, resolved through
// the same HostCapsFor holder the render-time input-kind gate reads — so any
// early consumer (the lint detector) reads identical capabilities to the
// authoritative gate, by construction, with no second capability table to drift.
// ok is false when the provider name binds to no shipped provider: an unknown,
// unimplemented (cloudformation), or eval provider, which a capability-based
// check skips rather than flags. When ok is true the returned capabilities are
// the provider's own ProviderCapabilities.
func CapabilitiesFor(providerName string) (renderer.ProviderCapabilities, bool) {
	caps, ok := HostCapsFor(providerName)
	if !ok {
		return renderer.ProviderCapabilities{}, false
	}
	return caps.Caps, true
}
