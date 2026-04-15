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
	Render(ctx context.Context, values map[string]interface{}, nodeCtx NodeRenderContext) (map[string][]byte, error)
}

// EngineFactory returns the Engine implementation for a given provider name
// (from `recipe.spec.provider`). Returning an error halts the build.
// Engines may be cached/singleton'd by the factory; the orchestrator makes no
// assumption about instance identity.
type EngineFactory func(providerName string) (Engine, error)

// BoundaryComposer is an optional interface for engines that produce
// boundary-level artifacts after all per-node Render calls for that boundary.
// The helm-chart provider uses this to emit an umbrella Chart.yaml + values.yaml
// listing each component as a sub-chart dependency. Engines that only produce
// flat per-node output (e.g. a future helm provider producing rendered K8s YAML)
// do not implement this.
type BoundaryComposer interface {
	// ComposeBoundary returns files relative to the boundary root.
	// The orchestrator prefixes them with `boundaries/<boundaryPath>/`.
	// Also signals whether per-node files should be nested under `charts/<nodeID>/`.
	ComposeBoundary(ctx context.Context, info BoundaryInfo) (files map[string][]byte, nestPerNode bool, err error)
}

// BoundaryInfo carries everything the BoundaryComposer needs.
type BoundaryInfo struct {
	BoundaryPath  string
	AggregateName string
	SubCharts     []SubChartInfo
}

// SubChartInfo describes one sub-chart contributing to an umbrella chart.
type SubChartInfo struct {
	InstanceID string                 // dot-notation node ID
	Version    string                 // resolved version
	Values     map[string]interface{} // per-sub-chart values for umbrella values.yaml
	Source     interface{}            // forwarded recipe.spec.source; nil for generative sub-charts
}

// NodeRenderContext provides identity metadata for manifest generation.
type NodeRenderContext struct {
	NodeID          string
	Labels          map[string]string
	Annotations     map[string]string // merged release annotations
	BoundaryPath    string
	SyncWave        string
	ResolvedVersion string
	Component       string
	Source          interface{} // forwarded from recipe.spec.source (nil / string path / structured)
	Provenance      ProvenanceContext
}

// ProvenanceContext carries build identity for resource labels.
type ProvenanceContext struct {
	CompilerVersion  string
	AggregateName    string
	AggregateVersion string
	ReleaseName      string
	Environment      string
}
