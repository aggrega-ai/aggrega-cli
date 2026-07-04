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
// assemblePolicyInput populates the structure on every build; the Step-4
// policy-validator socket marshals it to a configured policy plugin and reads
// the plugin's verdicts back.
type PolicyValidatorInput struct {
	Graph              types.IRGraph       `json:"graph"`
	PrecalculatedPaths []PrecalculatedPath `json:"precalculated_paths"`
}

// PolicyValidatorOutput is the JSON response shape the
// policy-validator.wasm guest emits. Results carries one [PolicyResult]
// per evaluated rule; an empty Results slice signals "no policy rules
// ran" (distinct from "all rules passed", which is a slice of Pass=true
// results). The Step-4 socket unmarshals this shape from the policy plugin
// and turns each failed result into a build-halting diagnostic.
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
