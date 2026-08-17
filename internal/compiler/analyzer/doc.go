// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package analyzer implements Step 4 of the compilation pipeline:
// the Static Analyzer & Validation Gate.
//
// # Role
//
// Step 4 consumes the IR graph emitted by Step 3 (internal/compiler/ir)
// and validates two classes of static contract:
//
//   - usesSchema selector rules on every edge — the target node's
//     labels must satisfy the source-side selector string declared on
//     the consuming Component's (or originating Feature's) usesSchema
//     entry for the socket being filled.
//   - Feature appliesTo constraints on every Feature attachment —
//     the target node's labels must satisfy the Feature catalog
//     entity's appliesTo / appliesToAnyOf constraints (AND across the
//     two surfaces; an omitted constraint is vacuously satisfied).
//
// Both checks delegate label matching to pkg/selector, and both report on
// [AnalysisResult].Errors, but they are not both evaluated in this package.
// The first is declared here as [EdgeTargetSatisfiesSocketSelector] and is
// evaluated over a presentation of the finalised graph, which this package is
// not handed; whatever holds that presentation invokes it and appends what it
// raised to the same slice, through [ErrorsFromObjections]. So a reader looking
// for the edge walk that enforces it will not find one here, and that is the
// arrangement rather than a gap.
//
// # Outputs
//
// Analyze returns one [AnalysisResult] carrying:
//
//   - Errors — every selector and appliesTo violation, one
//     [AnalysisError] per violation. The Errors slice is the gate
//     signal: a non-empty slice halts the pipeline before Step 5.
//   - Paths — the precalculated_paths array produced by cycle-safe
//     DFS through the IR graph (see [PrecalculatedPath]).
//   - PolicyInput — the [PolicyInput] payload destined for
//     the policy socket described in
//     aggrega-docs/architecture/14-extensibility.md.
//
// # Pipeline integration
//
// The build flow gates on len(AnalysisResult.Errors): a non-empty
// slice halts before Step 5, with one stderr line per violation.
// AnalysisResult.PolicyInput is computed on every build and, when a
// workspace configures a policy plugin, the pipeline marshals it to that
// plugin at Step 4 and appends each failed verdict to
// AnalysisResult.Errors, so a policy rejection halts the build through the
// same gate. AnalysisResult.Paths ride the PolicyInput to the plugin as
// precalculated reachability; they are otherwise not consumed by Step 5.
//
// # Determinism
//
// All output is byte-deterministic across runs of identical input:
//
//   - DFS adjacency lists sort alphabetically before traversal.
//   - DFS starts iterate node IDs in alphabetical order.
//   - PathIDs are assigned in deterministic-DFS-emission order
//     (path_01, path_02, …) — the index reflects traversal sequence
//     rather than a post-sort over the path set.
//   - AttachedFeatures on each path sort alphabetically.
//   - Socket-constraint violations emerge in the order the graph holds its
//     edges, which is the order the traversal invokes the rule in.
//   - AppliesTo-violation errors emerge in (nodeID, instance) order
//     by virtue of pre-sorted iteration in validateAppliesTo, with
//     no explicit post-sort.
package analyzer
