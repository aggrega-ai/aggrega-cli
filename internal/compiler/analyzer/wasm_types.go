// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import "github.com/cognitio/aggrega-cli/pkg/types"

// PolicyValidatorInput is the JSON payload Step 4 hands to the
// policy-validator.wasm socket described in
// aggrega-docs/architecture/14-extensibility.md (the Validation Gate
// row) and aggrega-docs/architecture/10-compilation-pipeline.md (the
// Step 4 mechanism). It carries a [PolicyGraph] subset of the IR plus
// the [PrecalculatedPath] array the analyzer enumerates alongside.
//
// **Status: contract definition only — not yet executed.** The Wasm
// runtime that would consume this payload is not wired into the
// pipeline; assemblePolicyInput populates the structure on every build
// for the sake of stable ABI surface. The shape becomes load-bearing
// once the runtime path lands.
type PolicyValidatorInput struct {
	Graph              PolicyGraph         `json:"graph"`
	PrecalculatedPaths []PrecalculatedPath `json:"precalculated_paths"`
}

// PolicyGraph is the IR subset exposed to the policy validator. Nodes
// is keyed by node ID; encoding/json sorts JSON object keys
// alphabetically at marshal time, so the wire payload is deterministic
// across runs of identical input even though the in-memory map type
// does not preserve iteration order. Edges inherits the ordering of
// IRGraph.Spec.Edges; the internal/compiler/ir package's doc.go
// documents that ordering's cross-package determinism contract.
type PolicyGraph struct {
	Nodes map[string]PolicyNode `json:"nodes"`
	Edges []PolicyEdge          `json:"edges"`
}

// PolicyNode is the per-node projection exposed to the policy validator.
// Four fields are populated:
//
//   - Component — the catalog component URI the node resolves against,
//     so policies can scope rules by component type.
//   - ResolvedVersion — the version the resolver pinned, so policies
//     can express version-floor or version-pin rules.
//   - Labels — the node's catalog and authoring labels, the vocabulary
//     architecture/06, /09, and /11 use to express compliance, AI
//     ontology, and topological-purity policy.
//   - Features — the per-instance Feature attachment map (instance
//     name → IRFeatureEntry), so policies can enforce required-feature
//     and forbidden-feature rules.
//
// The view is narrower than the full [types.IRNode] surface:
// BoundaryPath, SubsystemPath, SyncWave, the three annotation buckets,
// Provides, and SelfInject all live on IRNode but are not exposed
// here. Constraining the surface matches the principle-of-least-
// authority discipline that applies to every Wasm plugin socket;
// expanding the exposure if future policy authoring vocabularies need
// richer per-node metadata is a forward-looking decision tied to the
// runtime path landing.
type PolicyNode struct {
	Component       string                                 `json:"component"`
	ResolvedVersion string                                 `json:"resolved_version"`
	Labels          types.OrderedMap[string]               `json:"labels"`
	Features        types.OrderedMap[types.IRFeatureEntry] `json:"features,omitempty,omitzero"`
}

// PolicyEdge is the per-edge projection exposed to the policy validator.
// Source / Target / Socket carry the routing identity. OriginFeature
// carries the edge-atomicity marker that distinguishes Component-
// authored edges (empty OriginFeature) from Feature-authored edges
// (OriginFeature names the originating Feature instance), so policies
// can express rules sensitive to which authoring surface introduced
// the edge.
type PolicyEdge struct {
	Source        string `json:"source"`
	Target        string `json:"target"`
	Socket        string `json:"socket"`
	OriginFeature string `json:"origin_feature,omitempty"`
}

// PolicyValidatorOutput is the JSON response shape the
// policy-validator.wasm guest emits. Results carries one [PolicyResult]
// per evaluated rule; an empty Results slice signals "no policy rules
// ran" (distinct from "all rules passed", which is a slice of Pass=true
// results). Has no Go-side parser today — the Wasm runtime path that
// would invoke the guest is not wired into the pipeline.
type PolicyValidatorOutput struct {
	Results []PolicyResult `json:"results"`
}

// PolicyResult is one rule's verdict. Pass=false halts the build with
// Message surfaced as a build-time diagnostic; Pass=true is
// informational and does not affect build outcome.
type PolicyResult struct {
	Pass    bool   `json:"pass"`
	Message string `json:"message"`
}
