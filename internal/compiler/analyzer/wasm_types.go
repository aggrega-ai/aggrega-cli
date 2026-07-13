// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import "github.com/aggrega-ai/aggrega-cli/pkg/types"

// PolicyValidatorInput is the JSON payload Step 4 hands to the
// policy-validator.wasm socket. Graph carries the full provider-agnostic IR
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
	// Sources carries the definition files the attachment named, read by the
	// host at compile time and handed to the plugin verbatim, in an order sorted
	// by path. Each entry's path is the file's base name — never an absolute host
	// path — and its content is opaque bytes the host does not parse. Absent when
	// the attachment declares no sources.
	Sources []PolicySource `json:"sources,omitempty"`
	// Settings carries the free-form settings object the attachment declared,
	// handed to the plugin verbatim. The host does not parse or act on it. Nil
	// when the attachment declares no settings.
	Settings map[string]any `json:"settings,omitempty"`
}

// PolicySource is one definition file the host hands to a policy-validator
// plugin: its base-name Path and the file's Content. The host reads the file at
// compile time and carries its bytes verbatim; it never parses the content and
// never exposes an absolute host path in Path.
type PolicySource struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// PolicyValidatorOutput is the JSON response shape the
// policy-validator.wasm guest emits. Results carries the verdicts that fired
// during evaluation, one [PolicyResult] each; an allowing rule emits nothing,
// so an empty Results slice means no verdict fired. The policy socket
// unmarshals this shape from the policy plugin and turns each fired verdict
// into a build diagnostic.
type PolicyValidatorOutput struct {
	Results []PolicyResult `json:"results"`
}

// PolicyResult is one fired policy verdict: its Severity and a human-readable
// Message. Only fired verdicts appear in the output. Severity governs the build
// outcome: [SeverityDeny] halts the build, [SeverityWarn] flags the verdict and
// lets the build proceed, [SeverityAudit] records the verdict without affecting
// the outcome.
type PolicyResult struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// SeverityDeny, SeverityWarn, and SeverityAudit are the three verdict
// severities a [PolicyResult] may carry: deny halts the build, warn flags the
// verdict and lets the build proceed, audit records the verdict without
// affecting the build outcome.
const (
	SeverityDeny  = "deny"
	SeverityWarn  = "warn"
	SeverityAudit = "audit"
)

// RenderPolicyValidatorInput is the JSON payload the render stage hands to a
// policy-validator.wasm socket that declares the render stage. Artifacts
// carries the rendered output files the policy reasons over — each entry a
// path and its rendered content — as an ordered list sorted by path so the
// wire is byte-identical across runs of identical input. The render stage
// carries the rendered artifacts rather than the IR graph; a render-stage
// policy reasons over emitted output, not topology.
//
// The render-stage socket runner assembles this payload after render: it sorts
// the rendered files into Artifacts by path and populates Sources and Settings
// from the attachment. The wire shape and this Go mirror are single-sourced from
// the socket schema; the generated guest codec in aggrega-abi mirrors it.
type RenderPolicyValidatorInput struct {
	Artifacts []RenderedArtifact `json:"artifacts"`
	// Sources and Settings carry the same attachment-declared inputs as on the
	// IR-stage [PolicyValidatorInput]: the definition files read verbatim (paths
	// as base names, content opaque, sorted by path) and the free-form settings
	// object handed through untouched. Both are absent when the attachment
	// declares none.
	Sources  []PolicySource `json:"sources,omitempty"`
	Settings map[string]any `json:"settings,omitempty"`
}

// RenderedArtifact is one rendered output file in a render-stage policy input:
// its Path and the rendered Content at that path.
type RenderedArtifact struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}
