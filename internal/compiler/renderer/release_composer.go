// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// ReleaseComposer is the optional interface a provider engine implements to
// produce Release-level (cross-boundary) artifacts from per-node intermediate
// fragments. Where BoundaryComposer composes one boundary into umbrella
// files (helm-chart's umbrella Chart.yaml + values.yaml), ReleaseComposer
// composes the whole Release into top-level files (compose's single
// docker-compose.yaml at the Release output root).
//
// Lifecycle. After all per-node Phase A/B passes complete and after each
// boundary's BoundaryComposer (if any) has run, the orchestrator detects
// every distinct ReleaseComposer instance reachable across the Release's
// engines and calls ComposeRelease once per instance with a ReleaseInfo
// scoped to that engine's nodes. Returned files attach to the Release output
// root (no boundary prefix).
//
// Intermediate inputs. ComposeRelease reads fragments from
// ReleaseInfo.Boundaries[*].Nodes[*].IntermediateFiles — these are the
// EmittedFiles where Intermediate=true that the engine's per-node
// SynthesizeWorkload (or Render) produced. Intermediates are NOT written to
// disk by the orchestrator; ComposeRelease is the only consumer.
//
// Implemented by: compose engine (lands in synthesis-foundations WI06).
type ReleaseComposer interface {
	ComposeRelease(ctx context.Context, info ReleaseInfo) (files map[string][]byte, err error)
}

// ReleaseInfo is the shape ComposeRelease consumes — the cross-boundary view
// of one provider's contribution to the Release. The orchestrator builds one
// ReleaseInfo per ReleaseComposer instance, including only the nodes whose
// engine instance equals that ReleaseComposer.
type ReleaseInfo struct {
	// AggregateName is the top-level Aggregate this Release deploys (from
	// graph.Metadata.AggregateName). Useful for top-level metadata or
	// label embedding in the merged output.
	AggregateName string

	// ReleaseName, Environment carry Release-identity for downstream
	// metadata embedding (mirrors NodeRenderContext.Provenance).
	ReleaseName string
	Environment string

	// Boundaries is the per-boundary view of the contributing nodes,
	// ordered by boundary path (alphabetical) for output determinism.
	Boundaries []ReleaseBoundaryInfo

	// Edges carries the IR's directed "uses" edges across the entire
	// graph (the orchestrator threads graph.Spec.Edges through). Compose
	// uses this to infer depends_on between services. ReleaseComposer
	// implementations should filter to edges where both endpoints are in
	// their own Boundaries' nodes.
	Edges []types.IREdge
}

// ReleaseBoundaryInfo carries the contributing-node fragments for one
// boundary. The Annotations map is the union of CatalogAnnotations and
// ReleaseAnnotations on each node merged at boundary level (used by compose
// to derive a per-boundary network name from aggrega.ai/namespace).
type ReleaseBoundaryInfo struct {
	BoundaryPath string

	// Annotations is the per-node annotation map for nodes in this
	// boundary (NodeID → annotation map). The compose ReleaseComposer
	// reads aggrega.ai/namespace from these to derive a docker network
	// name per boundary.
	Annotations map[string]map[string]string

	// Nodes is the contributing-node list for this boundary, ordered by
	// NodeID alphabetical.
	Nodes []ReleaseNodeInfo
}

// ReleaseNodeInfo is one node's contribution to a ReleaseComposer's input.
// IntermediateFiles is the list of Intermediate=true EmittedFiles produced
// by that node — the per-node fragments the ReleaseComposer reads.
type ReleaseNodeInfo struct {
	NodeID string

	// ResolvedComponent is the fully qualified component reference
	// (componentType from the graph node) — useful when the
	// ReleaseComposer needs to emit per-component metadata.
	ResolvedComponent string

	// IntermediateFiles carries fragments produced by this node. For the
	// compose engine this is exactly one entry: <NodeID>.compose-fragment.yaml.
	// Other ReleaseComposer implementations may produce more.
	IntermediateFiles []EmittedFile
}
