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
// BuildIRContext is the orchestrator entry point. Alongside the graph it returns
// a separate FeatureOrder map that the renderer's Phase A reads to iterate
// FeatureRecipes in the deterministic cascade sequence — Go map iteration
// alone would not preserve that order.
//
// Conventions and contracts the package commits to:
//
//   - Per-node attachment cascade. collectNodeFeatures and
//     mergeContribution fold attachments from five sources in order:
//     sub-aggregate component, root-aggregate component, root-aggregate
//     subsystem, release boundary, release component-specific. The cascade
//     is the canonical merge order this package implements.
//   - Per-edge origin marker. NormalizedEdge.OriginFeature populated by
//     topology is copied into IREdge.OriginFeature so Feature-authored
//     edges remain separable from Component-authored edges in the IR.
//   - One attachment bucket. Every attachment a node holds lands in
//     IRSpec.FeatureValuesMapping whatever it produces, so a credential
//     Feature is merged by the same rules and projected through the same
//     shape as any other attachment. The `feature.aggrega.ai/kind: secret`
//     label is opaque metadata here; no branch in this package reads it.
//   - Annotation buckets. The IR keeps three independent annotation
//     buckets (catalog, release, aggregate), never merged into one. The
//     aggregate bucket is the Aggregate's subsystem annotations cascaded
//     onto the node with the component's own over them, which flattening
//     carries on the topology node; buildAnnotationBuckets copies it.
//   - Type-match fatal halt. ErrFeatureTypeMismatch fires when the same
//     attachment instance name carries divergent type URIs across
//     authoring surfaces. With one bucket per node the instance-name key
//     space is single, so this halt is what a same-name/different-type
//     collision resolves to — never a last-writer-wins merge.
//   - Determinism. Every iteration in this package that contributes to
//     IRGraph output runs in deterministic order: the topology's Nodes
//     OrderedMap preserves Aggregate author order; the edges slice is
//     inherited from topology in compositional author order — the
//     cross-product of component sequence × within-component `uses:`
//     socket order × within-socket alias order × per-node
//     attachment order — and is consumed without re-sorting; the
//     per-node attachment cascade iterates the topology's
//     already-ordered FeatureAttachments slice.
//
// Cross-package determinism contract. This package consumes
// topology.TopologyResult.Nodes and topology.TopologyResult.Edges as
// inputs without re-validating their ordering. The producer-side
// commitment from internal/compiler/topology has three points; the
// consumer-side trust mirrors them:
//
//   - Nodes are trusted in author order. BuildIRContext iterates topo.Nodes.All()
//     and builds IRSpec.Nodes in that order, propagating Aggregate
//     authoring order into the emitted IRGraph.
//   - Edges are trusted as the compositional cross-product (component
//     sequence × within-component `uses:` socket order × within-socket
//     alias order × per-node attachment order). BuildIRContext's
//     step 2 (the topology→IR edge conversion loop) walks topo.Edges
//     in slice order and emits IRSpec.Edges in the same order without
//     re-sorting; the user-visible IR JSONL output preserves the
//     cross-product across runs of identical input.
//   - Neither dimension is re-validated here. There is no in-package
//     guard on this side that would catch a topology-side regression —
//     a topology change that flipped Edges back to alphabetical order,
//     or flipped Nodes to alphabetical, would silently break IR emission
//     ordering and any downstream consumer that reads IRGraph as a
//     stable snapshot. The corresponding producer-side commitment lives
//     in the topology package's doc.go.
//
// Two flattened node IDs meeting is refused before the IR builder reads
// anything, by two conditions on the two paths that produce it. The topology
// builder halts with ErrSubAggregateNameCollision when merging a module's
// nodes would overwrite an entry already in the map, which current schema
// makes unreachable. Two subsystems of one Aggregate declaring a component
// under one name is ordinary YAML, and the rule declared over the flattened
// composition reports it. Either way, a compilation reaching BuildIRContext
// carries no silent overwrite.
//
// The package depends on `pkg/types`, `pkg/resolver`, and
// `internal/compiler/topology`; downstream consumers are
// `internal/compiler/renderer` (reads FeatureOrder + IRGraph) and
// `internal/compiler/analyzer` (reads IRGraph for static analysis).
package ir
