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
// Both checks delegate label matching to pkg/selector.
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
//   - PolicyInput — the [PolicyValidatorInput] payload destined for
//     the policy-validator.wasm socket described in
//     aggrega-docs/architecture/14-extensibility.md.
//
// # Pipeline integration
//
// The build flow gates on len(AnalysisResult.Errors): a non-empty
// slice halts before Step 5, with one stderr line per violation.
// AnalysisResult.Paths and AnalysisResult.PolicyInput are computed on
// every build but are not consumed by Step 5 today; the policy-
// validator Wasm runtime that would read them is not yet wired into
// the pipeline. Both surfaces ship as stable shape that becomes
// load-bearing once the Wasm runtime path lands.
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
//   - Selector-violation errors sort by (source, target, socket).
//   - AppliesTo-violation errors emerge in (nodeID, instance) order
//     by virtue of pre-sorted iteration in validateAppliesTo, with
//     no explicit post-sort.
package analyzer
