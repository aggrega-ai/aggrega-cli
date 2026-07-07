// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// HostCapabilities is the standalone holder of a provider's host-side
// capabilities — the compile-time behaviors the host runs even when a node's
// render and assembly route through a Wasm guest (codec selection, sub-chart
// projection, and the boundary->consumer env-override channel). It carries each
// capability as data rather than as a method on a concrete engine type, so the
// resolution the host performs is independent of any one engine's identity: a
// build resolves a provider's host capabilities from this holder rather than by
// type-asserting a concrete engine.
//
// A nil function field means the provider does not offer that capability, and
// the host takes the same fallback it takes for an engine that never implemented
// the corresponding optional interface — the stock YAML codec for a nil Codec,
// no umbrella sub-charts for a nil ProjectSubCharts, and no override for a nil
// BoundaryConsumerEnvOverrides.
//
// The input-kind capability set, the boundary-assembly mode, and the artifact-
// template delimiter pair are not carried here: those are the provider guest's
// own self-report, read from its live describe() descriptor at render time
// rather than from a host-side holder. Artifact placement (format decision,
// provenance placement, path) is the guest's concern too — the host projects the
// pre-evaluated artifact set and the guest places it.
type HostCapabilities struct {
	// Codec supplies the provider's own [frp.Codec] for the pipeline. Nil ⇒ the
	// provider borrows the core's stock YAML codec.
	Codec func(yamlIndent int) frp.Codec
	// ProjectSubCharts projects each sub-chart's identity, values, and pristine
	// vendored bytes into filesystem-free projections for an umbrella-assembling
	// provider. Nil ⇒ the provider assembles no umbrella sub-charts.
	ProjectSubCharts func(subCharts []SubChartInfo) ([]SubChartProjection, error)
	// BoundaryConsumerEnvOverrides drives the boundary->consumer env channel,
	// returning per-consumer environment overrides from a boundary-scope
	// FeatureRecipe's edge-enriched template context. Nil ⇒ no overrides.
	BoundaryConsumerEnvOverrides func(frCtx *types.TemplateContext) (map[string]map[string]string, error)
}

// HostCapable is implemented by anything that can report its host-side
// capabilities as a standalone [HostCapabilities] holder, independent of the
// concrete engine type. The in-tree Go engines implement it (returning their
// provider package's holder), and the Wasm provider wrapper implements it
// (returning the holder it captured at construction) — so the host resolves a
// provider's host-side capabilities the same way whether a node renders in-tree
// or through a Wasm guest, without the wrapper unwrapping to the concrete engine
// for capability purposes.
type HostCapable interface {
	HostCapabilities() HostCapabilities
}

// ResolveHostCaps returns engine's host-side capability holder, or the zero
// holder when engine reports none. It is the single home for host-side
// capability resolution: every host-side capability check reads its capability
// from the returned holder rather than type-asserting a concrete engine, so a
// provider's host capabilities resolve independently of the engine's identity.
// The zero holder's nil fields deliver the same fallbacks the host takes for an
// engine that implements no optional capability interface.
func ResolveHostCaps(engine Engine) HostCapabilities {
	if hc, ok := engine.(HostCapable); ok {
		return hc.HostCapabilities()
	}
	return HostCapabilities{}
}
