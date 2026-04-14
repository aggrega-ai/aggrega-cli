// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import "context"

// Engine renders one node's values dictionary into on-disk-style artifacts.
// Keys are relative paths (e.g., "Chart.yaml", "templates/api-deployment.yaml");
// values are file contents.
type Engine interface {
	Render(ctx context.Context, values map[string]interface{}, nodeCtx NodeRenderContext) (map[string][]byte, error)
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
