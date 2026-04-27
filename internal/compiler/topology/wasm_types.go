// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import "encoding/json"

// MutatorInput is the JSON payload that will be sent to the mutator.wasm
// plugin socket in Step 2 — the policy-mutation gate that may auto-tag
// nodes with compliance labels (e.g., applying SOC2 / GDPR / cost-center
// annotations driven by Release-level rules) before the IR builder
// consumes the topology.
//
// **Status: contract definition only — not executed.** The Go types
// in this file ship as the JSON ABI the future Wasm plugin runtime will
// bind against; no production code in this repository constructs or
// consumes them today. No importer of these types exists outside this
// package. The types are deliberately stable surface for downstream
// Wasm-runtime work to land against; `aggrega-cli` does not ship a Wasm
// runtime yet.
type MutatorInput struct {
	CompilationContext CompilationContext     `json:"compilation_context"`
	Graph              MutatorGraph           `json:"graph"`
}

// CompilationContext carries release-level metadata that the mutator
// plugin reads when deciding which compliance / FinOps / governance
// rules apply. Field naming follows snake_case to match the
// compiler-internal contract surface convention. Like the other types
// in this file, this struct is contract definition only — not yet
// executed.
type CompilationContext struct {
	ReleaseDestination string `json:"release_destination"`
	ReleaseName        string `json:"release_name"`
	Environment        string `json:"environment"`
}

// MutatorGraph is the subset of the topology graph exposed to the
// mutator plugin — labels-only, intentionally narrower than
// TopologyResult. The plugin does not need access to edges, values,
// or feature payloads to make labelling decisions; constraining the
// surface matches the principle-of-least-authority discipline that
// applies to every Wasm plugin socket.
type MutatorGraph struct {
	Nodes map[string]MutatorNode `json:"nodes"`
}

// MutatorNode is the minimum per-node projection the mutator plugin
// needs to evaluate compliance rules: which Component the node
// implements (Component), which version of that Component resolved
// (ResolvedVersion), and which labels the node carries (Labels). The
// projection is intentionally label-centric — the mutator's job is to
// add or rewrite labels, not to reshape the topology.
type MutatorNode struct {
	Component       string            `json:"component"`
	ResolvedVersion string            `json:"resolved_version"`
	Labels          map[string]string `json:"labels"`
}

// MutatorOutput is the JSON response shape the mutator.wasm plugin will
// emit: a slice of opaque JSON Patch documents. The host runtime
// applies each patch to the topology before handing the result to the
// IR builder. Patches stay opaque at the Go layer (`json.RawMessage`)
// so the host need not duplicate the JSON Patch parser the Wasm guest
// already implements; cross-validation is the runtime's job once the
// socket lands.
type MutatorOutput struct {
	Patches []json.RawMessage `json:"patches"`
}
