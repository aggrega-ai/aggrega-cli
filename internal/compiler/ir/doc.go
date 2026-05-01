// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package ir is Step 3 of the compiler pipeline — Graph Finalization. It
// consumes the upstream Step 2 output (a topology.TopologyResult, the
// flattened node graph with edges and feature attachments) and the Step 1
// output (a resolver.ResolveResult, the resolved Aggregate / Manifest /
// Release plus boundary infra) and emits a fully populated *types.IRGraph
// — the single in-memory shape that downstream Step 4 (static analyzer)
// and Step 5 (renderer) consume.
//
// BuildIR is the orchestrator entry point. Alongside the graph it returns
// a separate FeatureOrder map that the renderer's Phase A reads to iterate
// FeatureRecipes in the deterministic cascade sequence — Go map iteration
// alone would not preserve that order.
//
// Conventions and contracts the package commits to:
//
//   - Per-node feature cascade. collectNodeFeatures and mergeFeature
//     fold features from five sources in order: sub-aggregate component,
//     root-aggregate component, root-aggregate subsystem,
//     release boundary, release component-specific. The cascade is the
//     canonical merge order this package implements.
//   - Per-edge origin marker. NormalizedEdge.OriginFeature populated by
//     topology is copied into IREdge.OriginFeature so Feature-authored
//     edges remain separable from Component-authored edges in the IR.
//   - Credential routing. Credential Features (those carrying
//     `feature.aggrega.ai/kind: secret` on their catalog entity) route
//     into IRSpec.Secrets rather than IRSpec.FeatureValuesMapping; the
//     mergeFeature credential-routing branch implements the split.
//   - Annotation buckets. The IR keeps three independent annotation
//     buckets (catalog, release, aggregate). The aggregate bucket is
//     permanently empty today because the schema bans annotations on
//     Aggregate ComponentInstance blocks; buildAnnotationBuckets returns
//     the empty third value to keep the IRNode shape symmetric.
//   - Type-match fatal halt. ErrFeatureTypeMismatch fires when the same
//     Feature instance name carries divergent type URIs across
//     authoring surfaces.
//   - Determinism. Every iteration in this package that contributes to
//     IRGraph output runs in deterministic order: the topology's Nodes
//     OrderedMap preserves Aggregate author order; Markers are
//     accumulated into a set then sort-extracted; the edges slice is
//     inherited from topology already sorted alphabetical-by-source;
//     secrets and features per-node iterate the topology's
//     already-ordered attachments.
//
// Cross-package determinism contract. This package consumes
// topology.TopologyResult.Nodes and topology.TopologyResult.Edges as
// inputs without re-validating their ordering. The producer-side
// commitment from internal/compiler/topology has three points; the
// consumer-side trust mirrors them:
//
//   - Nodes are trusted in author order. BuildIR iterates topo.Nodes.All()
//     and builds IRSpec.Nodes in that order, propagating Aggregate
//     authoring order into the emitted IRGraph.
//   - Edges are trusted as alphabetical-by-source. BuildIR's step 2
//     (the topology→IR edge conversion loop) walks topo.Edges in slice
//     order and emits IRSpec.Edges in the same order without re-sorting.
//   - Neither dimension is re-validated here. There is no in-package
//     guard on this side that would catch a topology-side regression —
//     a topology change that flipped Edges back to author-order, or
//     flipped Nodes to alphabetical, would silently break IR emission
//     ordering and any downstream consumer that reads IRGraph as a
//     stable snapshot. The corresponding producer-side commitment lives
//     in the topology package's doc.go.
//
// The package depends on `pkg/types`, `pkg/resolver`, and
// `internal/compiler/topology`; downstream consumers are
// `internal/compiler/renderer` (reads FeatureOrder + IRGraph) and
// `internal/compiler/analyzer` (reads IRGraph for static analysis).
package ir
