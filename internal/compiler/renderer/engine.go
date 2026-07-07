// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"fmt"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// Engine renders one node's values dictionary into on-disk-style artifacts.
// Keys are relative paths (e.g., "Chart.yaml", "templates/deployment.yaml");
// values are file contents. Per-node files are later prefixed by the orchestrator
// with the boundary path (and, for chart-structure engines, a `charts/<nodeID>/`
// sub-chart prefix).
type Engine interface {
	Render(ctx context.Context, values map[string]any, nodeCtx NodeRenderContext) (map[string][]byte, error)
	// Capabilities reports which Recipe input kinds this engine renders. It is
	// the Go analog of the Wasm provider plugin's describe() ABI — each engine
	// self-reports, so the central input-kind gate (orchestrator) reads a
	// provider's support from the engine itself rather than a hardcoded table.
	Capabilities() ProviderCapabilities
}

// NodeInputValidator is the optional capability a provider engine implements to
// structurally validate a node's resolved render input before render dispatch.
// The central input-kind gate calls it once per node, immediately after the
// (mode, provider) capability check and before any Phase B render pass, so a
// provider that can detect a malformed input surfaces the defect as a Loud
// Guardrail before emission rather than producing broken output.
//
// The implementation returns the diagnostics the provider raised. An error
// return is an infrastructure failure (the guest could not run); a nil error
// with diagnostics is a completed check whose findings the caller routes by
// severity — an error-severity diagnostic halts the build (ErrProviderNodeInputInvalid,
// naming the node and provider), a warning or info diagnostic surfaces to the
// build result without changing rendered output. An engine that does not
// advertise the capability returns no diagnostics and no error, so the gate is a
// no-op for it.
type NodeInputValidator interface {
	ValidateNodeInput(ctx context.Context, values map[string]any, nodeCtx NodeRenderContext) ([]Diagnostic, error)
}

// CodecProvider is the optional capability an Engine implements to supply its
// own [frp.Codec] for the pipeline — how the provider's emitted output is
// carved into addressable units and reassembled byte-faithfully after a step
// edits some of them. An engine that does not implement it borrows the core's
// stock YAML codec ([NewStockYAMLCodec]); an engine that knows its own output
// shape (a templated format the YAML parser cannot tree directly, for instance)
// returns a codec that defines its own units.
//
// The pipeline executor type-asserts this capability off the engine rendering
// the scope it is carving, exactly as it asserts BoundaryAssembler and
// ExternalArtifactsHandler elsewhere: own-scope units use the own engine's
// codec, host-scope units use the host engine's codec. The returned codec is
// the provider's call about what is structured (a PATCH target) versus opaque,
// keeping that policy in the provider rather than the dumb core.
type CodecProvider interface {
	Codec(yamlIndent int) frp.Codec
}

// ProviderCapabilities declares which of the three Recipe input kinds a
// provider engine can render. This is the INPUT-KIND axis only — it is
// orthogonal to the output-assembly axis (BoundaryAssembler /
// ExternalArtifactsHandler, which describe how a node's output is assembled,
// not which input kinds it accepts).
//
//   - Sourced — renders a Recipe with a source: (sourced mode).
//   - Synthesized — renders a Recipe with a workload: (synthesised mode);
//     true iff the engine also implements WorkloadSynthesizer (pinned by test).
//   - ArtifactsStandalone — renders an artifacts-only Recipe (no source:, no
//     workload:) by emitting its declared artifacts as the node's output.
//
// The central gate consumes these to accept or reject a (node-mode, provider)
// combination once per node, with a single uniform diagnostic.
type ProviderCapabilities struct {
	Sourced             bool
	Synthesized         bool
	ArtifactsStandalone bool
}

// EngineFactory returns the Engine implementation for a given provider name
// (from `recipe.spec.provider`). Returning an error halts the build.
// Engines may be cached/singleton'd by the factory; the orchestrator makes no
// assumption about instance identity.
type EngineFactory func(providerName string) (Engine, error)

// ProviderVersionResolver reports the resolved provider-rendering plugin version
// behind a provider binding (recipe.spec.provider, version pin included), or the
// empty string when the binding renders through the in-tree engine with no
// resolved plugin version. It shares the provider→plugin resolution the engine
// factory performs, so the version reported here is the one behind the engine the
// factory returns for the same binding. A nil resolver reports every binding as
// version-less.
type ProviderVersionResolver func(providerRef string) string

// BoundaryAssembler is the optional interface a provider engine implements to
// assemble per-(evaluated_path, provider) artifacts from the per-node Render
// outputs at that dispatch path. The helm-chart provider uses this to emit an
// umbrella Chart.yaml + values.yaml listing each component as a sub-chart
// dependency; the compose provider uses it to merge per-node fragments into
// one docker-compose.yaml per dispatch path. Engines that only produce flat
// per-node output do not implement this.
//
// Replaces the previous two-tier (BoundaryComposer + ReleaseComposer) split.
// Per-(evaluated_path, provider) dispatch is the single assembly tier; the
// orchestrator groups contributors by dispatch key and invokes
// AssembleBoundary once per group.
type BoundaryAssembler interface {
	// AssembleBoundary returns files relative to the dispatch path.
	// The orchestrator prefixes them with `boundaries/<evaluated_path>/`
	// (or, post path-template-evaluation, the operator-authored path).
	// Also signals whether per-node files should be nested under
	// `charts/<nodeID>/`.
	AssembleBoundary(ctx context.Context, info BoundaryInfo) (files map[string][]byte, nestPerNode bool, err error)
}

// BoundaryAssemblyReporter is the optional interface an engine implements to
// report how it assembles a dispatch group's per-node output: "merge" folds the
// per-node fragments into one document, "nest" places each node's output under its
// own position in an umbrella structure, and "none" writes each node's output
// directly at the dispatch path. An empty value carries no assembly claim. A
// provider routed to a Wasm guest reports the value from the guest's live
// describe() self-report, so the host classifies render output from the same claim
// the guest advertises rather than from a host-side marker.
type BoundaryAssemblyReporter interface {
	BoundaryAssembly() string
}

// boundaryAssemblyOf returns engine's reported boundary-assembly mode, or the
// empty string when engine reports none. It is the single seam the classification
// helpers read, so an engine's assembly behaviour resolves independently of its
// concrete type.
func boundaryAssemblyOf(engine Engine) string {
	if r, ok := engine.(BoundaryAssemblyReporter); ok {
		return r.BoundaryAssembly()
	}
	return ""
}

// RenderIsIntermediate reports whether an engine's per-node render/synthesis
// output is intermediate input to a per-(path, provider) boundary assembly pass
// rather than a file written directly at the dispatch path. It is true only for a
// provider that folds its dispatch group's per-node fragments into one document
// (boundary assembly "merge"); a provider that nests each node under its own
// umbrella position ("nest") and a flat-output provider ("none") write per-node
// content directly and are not intermediate. It reads the engine's boundary-
// assembly self-report, so a provider routed to a Wasm guest classifies from the
// guest's live describe() claim. This is the single home of the
// intermediate-marking rule; every classification site reads it here.
func RenderIsIntermediate(engine Engine) bool {
	return boundaryAssemblyOf(engine) == wasm.BoundaryAssemblyMerge
}

// AssemblesBoundary reports whether an engine assembles its dispatch group's
// per-node output into a combined artifact — true for a provider that folds
// fragments into one document ("merge") or nests each node under its own umbrella
// position ("nest"), false for a flat-output provider ("none") or an engine that
// reports no assembly claim. The assembler election and the artifacts classifier
// read it to decide whether a dispatch group has an assembler.
func AssemblesBoundary(engine Engine) bool {
	switch boundaryAssemblyOf(engine) {
	case wasm.BoundaryAssemblyMerge, wasm.BoundaryAssemblyNest:
		return true
	default:
		return false
	}
}

// BoundaryInfo carries everything the BoundaryAssembler needs.
//
// BoundaryPath is the dispatch path the assembler writes into.
// AggregateName is the top-level Aggregate's name (for output identity).
// SubCharts is the per-sub-chart payload list (helm-chart umbrellas
// consume this to emit dependencies + sub-chart values; compose ignores).
//
// IntermediatesByNode carries the per-contributor Intermediate=true
// EmittedFiles produced by per-node Render. Compose reads these as
// fragments and merges into the assembled docker-compose.yaml; helmchart
// ignores (umbrella content comes from SubCharts).
//
// NodeAnnotations is per-node annotation map (NodeID → annotation map).
// Compose reads `aggrega.ai/namespace` from these to derive a docker
// network name per contributing boundary.
//
// NodeComponent maps NodeID → resolved component-type URI. Used by
// assemblers that need per-node component identity for output metadata.
//
// ContributingBoundaries lists every boundary path whose components fed
// into this dispatch group. Used for diagnostic context in error messages
// and for chart-name cascade lookups.
//
// Edges carries the IR's directed `uses` edges across the entire graph.
// Compose uses this to infer `depends_on` between services. Assemblers
// filter to edges where both endpoints are contributing-service names.
//
// ChartName is the cascaded `aggrega.ai/chart-name` annotation value
// when present (set by the orchestrator from the resolver's per-boundary
// annotation cascade for the helm-chart provider's merged-path case).
// Empty when no cascade value resolves; helmchart umbrella falls back to
// path-derived naming.
type BoundaryInfo struct {
	BoundaryPath           string
	AggregateName          string
	SubCharts              []SubChartInfo
	IntermediatesByNode    map[string][]EmittedFile
	NodeAnnotations        map[string]map[string]string
	NodeComponent          map[string]string
	ContributingBoundaries []string
	Edges                  []IREdgeInfo
	ChartName              string
	// YAMLIndent is the indentation width for the assembled artifact (clamped
	// to [2,9], default 2). Forwarded from RendererInput.
	YAMLIndent int
	// WarningSink, when non-nil, receives the non-fatal advisories a boundary
	// assembler raises while merging a dispatch group — the warning- and
	// info-severity diagnostics the guest returns in its assemble output, plus
	// any guest-emitted warnings. The orchestrator wires it to the build result's
	// warnings stream; assemblers nil-check it before calling. Warnings never
	// affect emitted bytes. Host-only: not part of any wire projection.
	WarningSink func(string)
}

// IREdgeInfo is the per-edge view passed to a BoundaryAssembler. Decoupled
// from types.IREdge so the renderer package's BoundaryAssembler input does
// not depend on pkg/types directly.
type IREdgeInfo struct {
	Source string
	Target string
}

// SubChartInfo describes one sub-chart contributing to an umbrella chart.
type SubChartInfo struct {
	InstanceID string         // dot-notation node ID
	Version    string         // resolved version
	Values     map[string]any // per-sub-chart values for umbrella values.yaml
	Source     any            // forwarded recipe.spec.source; nil for generative sub-charts
}

// SubChartProjection is the filesystem-free view of one sub-chart an
// umbrella-assembling engine hands to a Wasm guest. It carries the sub-chart's
// instance identity, the version the umbrella pins, the per-instance values the
// umbrella scopes under the sub-chart's alias, and — for a sub-chart vendored
// from a chart source — the pristine bytes of that chart's Chart.yaml, read from
// the local filesystem at compile time. A generative sub-chart carries no
// ChartYAML. A guest assembles the umbrella from these views alone and touches no
// filesystem.
type SubChartProjection struct {
	InstanceID string
	Version    string
	Values     map[string]any
	ChartYAML  []byte
}

// SubChartProjector is the optional interface an umbrella-assembling engine
// implements to project each sub-chart's identity, per-instance values, and the
// pristine bytes of its vendored Chart.yaml — read from the local filesystem at
// compile time — into filesystem-free SubChartProjections, so a Wasm guest
// assembles the umbrella from pure data and touches no filesystem.
//
// The wasmengine adapter type-asserts the wrapped Go engine to this interface
// before populating the assemble wire's sub-chart carriers: an engine that does
// not implement it contributes no sub-chart projection, so the adapter stays
// provider-agnostic (it asks "do you project sub-charts?", not "are you
// helm-chart?") and a flat-assembling provider's wire carries no umbrella
// scalars. The host performs the pristine read; the adapter never touches the
// filesystem itself.
type SubChartProjector interface {
	ProjectSubCharts(subCharts []SubChartInfo) ([]SubChartProjection, error)
}

// SplitDiagnostics partitions a provider guest's diagnostics by severity into
// the human-readable messages of its error-severity entries and the messages of
// its warning- and info-severity entries, each list in the guest's emission
// order. It is the single classification the render, assemble, and input-validation
// surfaces share, so a diagnostic's severity means the same thing on every
// provider-socket crossing: "error" halts, anything else advises. An entry with
// an unrecognized severity is treated as an advisory (non-halting) so a guest
// cannot halt the build with a severity the host does not model.
func SplitDiagnostics(diags []Diagnostic) (errs []string, advisories []string) {
	for _, d := range diags {
		if d.Severity == "error" {
			errs = append(errs, d.Message)
			continue
		}
		advisories = append(advisories, d.Message)
	}
	return errs, advisories
}

// NodeRenderContext provides identity metadata for manifest generation.
type NodeRenderContext struct {
	NodeID       string
	Annotations  map[string]string // merged release annotations
	BoundaryPath string
	SyncWave     int // deployment wave from the containing boundary's syncWave (signed integer; default 0)
	// Source is what engines actually consume (via loader.Load or
	// equivalent): the resolved filesystem path for remote sources
	// (extracted directory under the source-artifact cache slot),
	// the verbatim path for local sources (Tier-1 filesystem paths,
	// Tier-2 workspace-relative paths), or nil for sourceless
	// Recipes. The orchestrator populates this before dispatch;
	// engines that receive a non-string value here treat it as an
	// orchestrator bug rather than authoring drift.
	Source any
	// OriginalSource carries the verbatim Recipe spec.source as
	// authored, regardless of whether Source was resolved through
	// the cache. Engines reference OriginalSource for human-readable
	// diagnostics ("chart load failed for oci://...") so error
	// messages match the operator's source code rather than the
	// opaque cache path. Engines NEVER call loader.Load on
	// OriginalSource — Source is the load-bearing path; the helper
	// sourceForDiagnostic centralises the diagnostic formatting.
	OriginalSource any
	RecipeName     string // recipe metadata.name; empty when no Recipe selected for this node
	RecipeVersion  string // recipe metadata.version; pairs with RecipeName to form the aggrega.ai/recipe-uri label
	// ProviderName is the bare provider engine kind rendering this node
	// (recipe.spec.provider with any version pin stripped). When set it is the
	// value of the aggrega.ai/provider label.
	ProviderName string
	// ProviderVersion is the resolved provider-rendering plugin version behind
	// ProviderName, or empty when the node renders through the in-tree engine
	// with no resolved plugin version. When set it is the value of the
	// aggrega.ai/provider-version label; empty omits that label.
	ProviderVersion string
	// LabelComponentID, when non-empty, overrides NodeID as the value of the
	// app.kubernetes.io/name and aggrega.ai/component-id labels. Used by FR-
	// emitted resources whose internal NodeID carries marker syntax
	// (e.g. <boundary-fr>:bridge@cluster) that needs sanitization for K8s
	// label-value rules. Empty means "use NodeID directly" — the default for
	// regular component nodes whose NodeIDs are already alphanumeric.
	LabelComponentID string
	Provenance       ProvenanceContext
	// YAMLIndent is the indentation width for artifacts rendered from this
	// node (clamped to [2,9], default 2). Forwarded from RendererInput.
	YAMLIndent int
	// WarningSink, when non-nil, receives the non-fatal advisories a provider
	// engine raises while rendering this node — the warning- and info-severity
	// diagnostics the guest returns in its render output, plus any guest-emitted
	// warnings. The orchestrator wires it to the build result's warnings stream;
	// engines nil-check it before calling. Warnings never affect emitted bytes,
	// so byte-identity of rendered files is independent of whether a sink is
	// attached. Host-only: it is not part of any wire projection sent to a guest.
	WarningSink func(string)
}

// ProvenanceContext carries build identity for resource labels.
type ProvenanceContext struct {
	CompilerVersion  string
	AggregateName    string
	AggregateVersion string
	ReleaseName      string
	Environment      string
}

// SourceForDiagnostic returns a human-readable string identifying
// nodeCtx's source for use in engine error messages. Prefers
// OriginalSource (the verbatim Recipe spec.source — what the
// operator authored, e.g. "oci://hub.aggrega.ai/charts/mariadb")
// over Source (the resolved cache path, opaque to operators).
// Falls back to Source when OriginalSource is unset (sourceless
// recipes, local-path recipes, legacy code paths).
//
// Returns "<sourceless>" when both fields are nil so error
// messages stay self-describing.
func SourceForDiagnostic(nodeCtx NodeRenderContext) string {
	if nodeCtx.OriginalSource != nil {
		if s, ok := nodeCtx.OriginalSource.(string); ok {
			return s
		}
		return fmt.Sprintf("%+v", nodeCtx.OriginalSource)
	}
	if nodeCtx.Source != nil {
		if s, ok := nodeCtx.Source.(string); ok {
			return s
		}
		return fmt.Sprintf("%+v", nodeCtx.Source)
	}
	return "<sourceless>"
}
