// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import "github.com/aggrega-ai/aggrega-cli/pkg/types"

// PolicyValidatorInput is the JSON payload Step 4 hands to the
// policy-validator.wasm socket. Graph carries the full target-agnostic IR
// graph — the same [types.IRGraph] the Topology Builder emits — so the policy
// plugin reasons over the complete node, edge, and attachment detail the
// compiler holds rather than a narrow projection. PrecalculatedPaths carries
// the routing paths the analyzer enumerates alongside so policies can reason
// about reachability without re-walking the graph. The wire shape is
// single-sourced from ir-v1alpha1.schema.json (referenced by
// wasm-policy-validator-v1alpha1.schema.json); the generated guest codec in
// aggrega-abi mirrors it.
//
// **Status: contract definition only — not yet executed.** The Wasm runtime
// that would consume this payload is not wired into the pipeline;
// assemblePolicyInput populates the structure on every build for the sake of
// stable ABI surface. The shape becomes load-bearing once the runtime path
// lands.
type PolicyValidatorInput struct {
	Graph              types.IRGraph       `json:"graph"`
	PrecalculatedPaths []PrecalculatedPath `json:"precalculated_paths"`
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
