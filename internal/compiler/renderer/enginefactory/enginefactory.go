// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package enginefactory provides the compiler's render routing: the
// catalog-driven Wasm engine factory (NewHybrid) that resolves a provider name to
// its serving provider-rendering plugin, and the input-kind capability accessor
// (CapabilitiesFor) an early lint detector reads. Both the CLI build and the
// build-faithful test harness consume it, so a test routes providers exactly as a
// real build does. A provider's rendering behaviour — its input kinds, its
// boundary-assembly mode, its codec, and its optional export surface — is the
// guest's own describe() self-report; the compiler carries no per-provider host
// Go for a standard provider.
package enginefactory

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

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
