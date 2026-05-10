// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import "context"

// Engine renders one node's values dictionary into on-disk-style artifacts.
// Keys are relative paths (e.g., "Chart.yaml", "templates/deployment.yaml");
// values are file contents. Per-node files are later prefixed by the orchestrator
// with the boundary path (and, for chart-structure engines, a `charts/<nodeID>/`
// sub-chart prefix).
type Engine interface {
	Render(ctx context.Context, values map[string]any, nodeCtx NodeRenderContext) (map[string][]byte, error)
}

// EngineFactory returns the Engine implementation for a given provider name
// (from `recipe.spec.provider`). Returning an error halts the build.
// Engines may be cached/singleton'd by the factory; the orchestrator makes no
// assumption about instance identity.
type EngineFactory func(providerName string) (Engine, error)

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

// NodeRenderContext provides identity metadata for manifest generation.
type NodeRenderContext struct {
	NodeID          string
	Labels          map[string]string
	Annotations     map[string]string // merged release annotations
	BoundaryPath    string
	SyncWave        int // deployment wave from the containing boundary's syncWave (signed integer; default 0)
	ResolvedVersion string
	Component       string
	Source          any    // forwarded from recipe.spec.source (nil / string path / structured)
	RecipeName      string // recipe metadata.name; empty when no Recipe selected for this node
	RecipeVersion   string // recipe metadata.version; pairs with RecipeName to form the aggrega.ai/recipe-uri label
	// LabelComponentID, when non-empty, overrides NodeID as the value of the
	// app.kubernetes.io/name and aggrega.ai/component-id labels. Used by FR-
	// emitted resources whose internal NodeID carries marker syntax
	// (e.g. <boundary-fr>:bridge@cluster) that needs sanitization for K8s
	// label-value rules. Empty means "use NodeID directly" — the default for
	// regular component nodes whose NodeIDs are already alphanumeric.
	LabelComponentID string
	Provenance       ProvenanceContext
}

// ProvenanceContext carries build identity for resource labels.
type ProvenanceContext struct {
	CompilerVersion  string
	AggregateName    string
	AggregateVersion string
	ReleaseName      string
	Environment      string
}
