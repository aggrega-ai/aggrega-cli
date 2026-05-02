// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"github.com/cognitio/aggrega-cli/pkg/resolver"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// AnalysisResult is the bundled output of [Analyze]. The three fields
// carry the artifacts produced by Step 4's two label-matching classes
// and its precalculated_paths / policy-input assembly:
//
//   - Errors — every selector and appliesTo violation discovered, one
//     [AnalysisError] per violation. Build flow gates on
//     len(Errors) > 0; see the package doc Pipeline Integration
//     subsection for the gating semantics.
//   - Paths — the cycle-safe DFS enumeration of acyclic paths through
//     the IR graph; see [calculatePaths] for the contract.
//   - PolicyInput — the policy-validator.wasm payload (always
//     non-nil); see [PolicyValidatorInput] for the ABI shape and
//     not-yet-executed status.
type AnalysisResult struct {
	Errors      []AnalysisError
	Paths       []PrecalculatedPath
	PolicyInput *PolicyValidatorInput
}

// AnalysisError is one violation discovered during Step 4. The struct
// is shaped for edge-style violations (the selector check) and reused
// for per-Feature-attachment violations (the appliesTo check); the
// fields carry slightly different semantics across the two classes:
//
//   - Selector violations (from validateSelectors): Source is the
//     source nodeID of the violating edge, Target is the target
//     nodeID, Socket is the socket name, Selector is the selector
//     string that failed to match (or whose parse failed), Message
//     is a human-readable diagnostic.
//   - AppliesTo violations (from validateAppliesTo): Source is the
//     target nodeID of the Feature attachment, Target is the Feature
//     instance name, Socket is empty, Selector is [formatAppliesTo]'s
//     summary of the constraint set, Message is a human-readable
//     diagnostic.
//
// Callers that branch on violation class can distinguish them by
// inspecting Socket: a non-empty Socket signals a selector violation;
// an empty Socket signals an appliesTo violation.
type AnalysisError struct {
	Source   string
	Target   string
	Socket   string
	Selector string
	Message  string
}

// PrecalculatedPath is one acyclic path through the IR graph,
// enumerated by [calculatePaths]'s cycle-safe DFS:
//
//   - PathID — gap-free monotonic identifier in DFS-emission order
//     (path_01, path_02, …) across the entire output, not numbered
//     per start node.
//   - Nodes — the node-ID sequence from the start node to a terminal
//     or cycle-blocked node, length always > 1 (single-node and
//     dead-end starts are not emitted).
//   - AttachedFeatures — the alphabetically sorted union of feature
//     types attached to any node along the path. Set-membership
//     semantics: a feature appears once even if multiple nodes
//     carry it. Empty when no node along the path carries any
//     Feature attachment.
type PrecalculatedPath struct {
	PathID           string   `json:"path_id"`
	Nodes            []string `json:"nodes"`
	AttachedFeatures []string `json:"attached_features"`
}

// Analyze executes Step 4 of the compiler pipeline. It returns a
// non-nil [AnalysisResult] populated with:
//
//  1. Selector-violation errors from validateSelectors.
//  2. AppliesTo-violation errors from validateAppliesTo (appended
//     to the same Errors slice).
//  3. Precalculated paths from calculatePaths.
//  4. The PolicyValidatorInput payload from assemblePolicyInput.
//
// Build flow gates on len(result.Errors) > 0 before invoking Step 5.
// AnalysisResult.Paths and AnalysisResult.PolicyInput are computed
// unconditionally; see the package doc Pipeline Integration
// subsection for their downstream consumption status.
func Analyze(graph *types.IRGraph, catalogLookup resolver.CatalogLookup) *AnalysisResult {
	// 1. Selector validation (edge @selector rules)
	errs := validateSelectors(graph, catalogLookup)

	// 2. Feature appliesTo validation
	errs = append(errs, validateAppliesTo(graph, catalogLookup)...)

	// 3. DFS path calculation
	paths := calculatePaths(graph)

	// 4. Assemble policy input
	policyInput := assemblePolicyInput(graph, paths)

	return &AnalysisResult{
		Errors:      errs,
		Paths:       paths,
		PolicyInput: policyInput,
	}
}

// assemblePolicyInput projects the IR graph into the
// [PolicyValidatorInput] ABI shape. The projection is narrower than
// the IR by design (per the principle-of-least-authority discipline
// applied to every Wasm plugin socket) and flattens
// IRGraph.Spec.Nodes from OrderedMap to plain map[string]PolicyNode;
// encoding/json alphabetises JSON object keys at marshal time, so
// the wire payload stays deterministic across runs of identical
// input even though the in-memory map type does not preserve
// iteration order. IRGraph.Spec.Edges is forwarded as a slice with
// field-narrowing; the per-node Features OrderedMap is forwarded
// preserving author order.
func assemblePolicyInput(graph *types.IRGraph, paths []PrecalculatedPath) *PolicyValidatorInput {
	policyNodes := make(map[string]PolicyNode, graph.Spec.Nodes.Len())
	for id, node := range graph.Spec.Nodes.All() {
		pn := PolicyNode{
			Component:       node.Component,
			ResolvedVersion: node.ResolvedVersion,
			Labels:          node.Labels,
		}
		if fvm, ok := graph.Spec.FeatureValuesMapping.Get(id); ok {
			pn.Features = fvm
		}
		policyNodes[id] = pn
	}

	policyEdges := make([]PolicyEdge, 0, len(graph.Spec.Edges))
	for _, e := range graph.Spec.Edges {
		policyEdges = append(policyEdges, PolicyEdge{
			Source:        e.Source,
			Target:        e.Target,
			Socket:        e.Socket,
			OriginFeature: e.OriginFeature,
		})
	}

	return &PolicyValidatorInput{
		Graph: PolicyGraph{
			Nodes: policyNodes,
			Edges: policyEdges,
		},
		PrecalculatedPaths: paths,
	}
}
