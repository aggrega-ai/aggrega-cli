// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package enginefactory provides the canonical provider-name -> renderer.Engine
// dispatch. It is the single source of truth for which provider names the
// compiler renders and which engine each maps to; both the CLI build and the
// build-faithful test harness consume it, so a test renders through exactly the
// engines a real build uses.
package enginefactory

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	renderCompose "github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/compose"
	renderHelm "github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helm"
	renderHelmChart "github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helmchart"
)

// Dispatch maps a recipe.spec.provider name to a fresh-per-call renderer.Engine.
// It satisfies renderer.EngineFactory. Three provider names ship:
//
//   - "helm" — fresh HelmEngine per call. The empty-string branch also routes
//     here, a defensive default; the Recipe schema requires spec.provider, so
//     empty values do not reach this dispatch in normal flow.
//   - "helm-chart" — fresh helmchart.HelmEngine per call.
//   - "compose" — fresh ComposeEngine per call.
//
// Engines are stateless; per-(path, provider) aggregation happens in the
// orchestrator. An unknown providerName wraps renderer.ErrUnknownProvider with
// the offending name so the upstream RenderAll error surfaces with dispatch
// context intact.
func Dispatch(providerName string) (renderer.Engine, error) {
	switch providerName {
	case "helm", "":
		return &renderHelm.HelmEngine{}, nil
	case "helm-chart":
		return &renderHelmChart.HelmEngine{}, nil
	case "compose":
		return &renderCompose.ComposeEngine{}, nil
	default:
		return nil, fmt.Errorf("%w: %q", renderer.ErrUnknownProvider, providerName)
	}
}

// HostCapsFor returns a provider's host-side capability holder by name, resolved
// from the provider package's own holder function — the same holder the in-tree
// engine reports through renderer.HostCapable, so the Wasm route and the in-tree
// route read one source and cannot drift. It obtains the holder without
// constructing the provider's Go engine, so the Wasm render route (which takes a
// holder, not an engine) never depends on a constructed engine instance. The
// empty-string branch mirrors Dispatch's defensive default to "helm". ok is false
// for a provider name that binds to no shipped provider, matching Dispatch's
// unknown-name contract.
func HostCapsFor(providerName string) (renderer.HostCapabilities, bool) {
	switch providerName {
	case "helm", "":
		return renderHelm.HostCaps(), true
	case "helm-chart":
		return renderHelmChart.HostCaps(), true
	case "compose":
		return renderCompose.HostCaps(), true
	default:
		return renderer.HostCapabilities{}, false
	}
}

// CapabilitiesFor reports a provider's engine capabilities, resolved through the
// same Dispatch the render-time input-kind gate uses — so any early consumer
// (the lint detector) reads identical capabilities to the authoritative gate, by
// construction, with no second capability table to drift. ok is false when the
// provider name binds to no shipped engine (Dispatch returns ErrUnknownProvider):
// an unknown, unimplemented (terraform/cloudformation), or eval provider, which a
// capability-based check skips rather than flags. When ok is true the returned
// capabilities are the engine's own ProviderCapabilities.
func CapabilitiesFor(providerName string) (renderer.ProviderCapabilities, bool) {
	engine, err := Dispatch(providerName)
	if err != nil {
		return renderer.ProviderCapabilities{}, false
	}
	return engine.Capabilities(), true
}
