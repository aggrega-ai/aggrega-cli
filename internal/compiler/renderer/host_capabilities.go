// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// HostCapabilities is the standalone holder of a provider's host-side
// capabilities — the compile-time behaviors the host runs even when a node's
// render and assembly route through a Wasm guest (codec selection, source and
// sub-chart projection, external-artifacts placement, and the boundary->consumer
// env-override channel). It carries each capability as data rather than as a
// method on a concrete engine type, so the resolution the host performs is
// independent of any one engine's identity: a build resolves a provider's host
// capabilities from this holder rather than by type-asserting a concrete engine.
//
// A nil function field (or a false marker) means the provider does not offer
// that capability, and the host takes the same fallback it takes for an engine
// that never implemented the corresponding optional interface — the stock YAML
// codec for a nil Codec, the bare basename for a nil PathForArtifact, the
// default "{{"/"}}" delimiters for a nil ArtifactDelimiters, and no override for
// a nil BoundaryConsumerEnvOverrides.
//
// The input-kind capability set and the boundary-assembly mode are not carried
// here: those are the provider guest's own self-report, read from its live
// describe() descriptor at render time rather than from a host-side holder.
type HostCapabilities struct {
	// Codec supplies the provider's own [frp.Codec] for the pipeline. Nil ⇒ the
	// provider borrows the core's stock YAML codec.
	Codec func(yamlIndent int) frp.Codec
	// ProjectSource reads the provider's source from the local filesystem at
	// compile time and packs it into a SourceProjection. Nil ⇒ the provider is
	// not a sourced provider.
	ProjectSource func(path string) (*SourceProjection, error)
	// ProjectSubCharts projects each sub-chart's identity, values, and pristine
	// vendored bytes into filesystem-free projections for an umbrella-assembling
	// provider. Nil ⇒ the provider assembles no umbrella sub-charts.
	ProjectSubCharts func(subCharts []SubChartInfo) ([]SubChartProjection, error)
	// ExternalArtifacts marks a provider that handles artifacts outside the
	// standard per-node emission path (merging $import:-loaded entries into the
	// chart's templates/ directory). It is the data form of the
	// ExternalArtifactsHandler marker.
	ExternalArtifacts bool
	// PathForArtifact declares the per-node-relative path an emitted
	// $import:-loaded artifact lands at for an ExternalArtifacts provider. Nil ⇒
	// the bare basename.
	PathForArtifact func(nodeCtx NodeRenderContext, basename string) string
	// ArtifactDelimiters declares the Go-template delimiter pair Aggrega's
	// pre-templating uses on each $import:-loaded artifact file for an
	// ExternalArtifacts provider. Nil ⇒ the default "{{"/"}}" pair.
	ArtifactDelimiters func() (left, right string)
	// VerbatimArtifacts marks a provider whose artifact bodies emit
	// byte-for-byte after Go-template evaluation, skipping the YAML-parse-and-
	// provenance step and accepting inline entries. It is the data form for a
	// flat text-emitting provider like terraform, whose config (HCL or
	// .tf.json) is not Kubernetes YAML and so must not be round-tripped through
	// a YAML parse or have provenance labels spliced in. It is orthogonal to
	// ExternalArtifacts: a verbatim provider still routes through the
	// external-handler branch for delimiter and placement handling, but its
	// per-entry evaluation stops after template evaluation and returns the
	// rendered bytes unchanged. False ⇒ the standard YAML-parse-and-provenance
	// emission path.
	VerbatimArtifacts bool
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
// The zero holder's nil fields and false markers deliver the same fallbacks the
// host takes for an engine that implements no optional capability interface.
func ResolveHostCaps(engine Engine) HostCapabilities {
	if hc, ok := engine.(HostCapable); ok {
		return hc.HostCapabilities()
	}
	return HostCapabilities{}
}
