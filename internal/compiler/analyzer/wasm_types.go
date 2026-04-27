// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import "github.com/cognitio/aggrega-cli/pkg/types"

// PolicyValidatorInput is the payload sent to the policy-validator.wasm socket in Step 4.
// Contract definition only — not yet executed.
type PolicyValidatorInput struct {
	Graph              PolicyGraph         `json:"graph"`
	PrecalculatedPaths []PrecalculatedPath `json:"precalculated_paths"`
}

// PolicyGraph is the IR graph subset exposed to the policy validator.
type PolicyGraph struct {
	Nodes map[string]PolicyNode `json:"nodes"`
	Edges []PolicyEdge          `json:"edges"`
}

// PolicyNode is one node visible to the policy validator.
type PolicyNode struct {
	Component       string                              `json:"component"`
	ResolvedVersion string                              `json:"resolved_version"`
	Labels          types.OrderedMap[string]            `json:"labels"`
	Features        types.OrderedMap[types.IRFeatureEntry] `json:"features,omitempty,omitzero"`
}

// PolicyEdge is one edge visible to the policy validator.
type PolicyEdge struct {
	Source        string `json:"source"`
	Target        string `json:"target"`
	Socket        string `json:"socket"`
	OriginFeature string `json:"origin_feature,omitempty"`
}

// PolicyValidatorOutput is the response from the policy-validator.wasm plugin.
type PolicyValidatorOutput struct {
	Results []PolicyResult `json:"results"`
}

// PolicyResult is one validation result from the policy validator.
type PolicyResult struct {
	Pass    bool   `json:"pass"`
	Message string `json:"message"`
}
