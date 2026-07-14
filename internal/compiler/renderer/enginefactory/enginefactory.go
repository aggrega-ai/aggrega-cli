// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package enginefactory provides the compiler's render routing: the
// catalog-driven Wasm engine factory (NewHybrid) that resolves a provider name to
// its serving provider-rendering plugin, and the input-kind capability accessor
// (ContractFor) an early lint detector reads. Both the CLI build and the
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

// ContractFor reports a provider's input-kind capabilities from the
// authoring-time render-behavior contract on the provider-rendering Plugin entity
// that serves the provider — the same contract the load gate cross-checks against
// the guest's describe() self-report. It resolves the entity through the given
// providerPlugin lookup (the provider-name → serving Plugin resolution the render
// route shares), so an early consumer (the lint detector) reads the published
// contract without loading a guest. ok is false when the lookup is nil, no
// serving provider Plugin resolves, the resolution errors, or the resolved Plugin
// carries no render-behavior contract: an input-kind check skips rather than
// flags, leaving the render-time gate as the authoritative backstop.
func ContractFor(providerPlugin func(ref string) (types.Plugin, bool, error), providerName string) (renderer.ProviderContract, bool) {
	if providerPlugin == nil {
		return renderer.ProviderContract{}, false
	}
	plugin, ok, err := providerPlugin(providerName)
	if err != nil || !ok || plugin.Spec.Provider == nil || plugin.Spec.Provider.Contract == nil {
		return renderer.ProviderContract{}, false
	}
	ik := plugin.Spec.Provider.Contract.InputKinds
	return renderer.ProviderContract{
		Sourced:             ik.Sourced,
		Synthesized:         ik.Synthesized,
		ArtifactsStandalone: ik.ArtifactsStandalone,
	}, true
}
