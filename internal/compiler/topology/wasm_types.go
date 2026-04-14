// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import "encoding/json"

// MutatorInput is the payload sent to the mutator.wasm socket in Step 2.
// The Wasm plugin may auto-tag nodes with compliance labels.
// Not yet executed — struct definition only.
type MutatorInput struct {
	CompilationContext CompilationContext     `json:"compilation_context"`
	Graph              MutatorGraph           `json:"graph"`
}

// CompilationContext carries release-level metadata for policy evaluation.
type CompilationContext struct {
	ReleaseDestination string `json:"release_destination"`
	ReleaseName        string `json:"release_name"`
	Environment        string `json:"environment"`
}

// MutatorGraph is the subset of the topology graph exposed to the mutator plugin.
type MutatorGraph struct {
	Nodes map[string]MutatorNode `json:"nodes"`
}

// MutatorNode is one node visible to the mutator plugin.
type MutatorNode struct {
	Component       string            `json:"component"`
	ResolvedVersion string            `json:"resolved_version"`
	Labels          map[string]string `json:"labels"`
}

// MutatorOutput is the response from the mutator.wasm plugin.
type MutatorOutput struct {
	Patches []json.RawMessage `json:"patches"`
}
