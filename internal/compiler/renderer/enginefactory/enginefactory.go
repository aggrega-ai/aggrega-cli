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
