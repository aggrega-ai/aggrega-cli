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
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
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

// CapabilitiesFor reports a provider's input-kind capabilities from the
// authoring-time capabilities claim on the provider-rendering Plugin entity that
// serves the provider — the same claim the load gate cross-checks against the
// guest's describe() self-report. It resolves the entity through the given
// providerPlugin lookup (the provider-name → serving Plugin resolution the render
// route shares), so an early consumer (the lint detector) reads the published
// capability claim without loading a guest. ok is false when the lookup is nil, no
// serving provider Plugin resolves, the resolution errors, or the resolved Plugin
// carries no capabilities claim: a capability-based check skips rather than flags,
// leaving the render-time gate as the authoritative backstop.
func CapabilitiesFor(providerPlugin func(ref string) (types.Plugin, bool, error), providerName string) (renderer.ProviderCapabilities, bool) {
	if providerPlugin == nil {
		return renderer.ProviderCapabilities{}, false
	}
	plugin, ok, err := providerPlugin(providerName)
	if err != nil || !ok || plugin.Spec.Capabilities == nil {
		return renderer.ProviderCapabilities{}, false
	}
	ik := plugin.Spec.Capabilities.InputKinds
	return renderer.ProviderCapabilities{
		Sourced:             ik.Sourced,
		Synthesized:         ik.Synthesized,
		ArtifactsStandalone: ik.ArtifactsStandalone,
	}, true
}
