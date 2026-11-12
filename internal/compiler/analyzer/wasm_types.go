// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package analyzer

import (
	"encoding/json"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// PolicyInput is the payload Step 4 hands to the policy socket. Graph
// carries the full provider-agnostic IR graph — the same [types.IRGraph] the
// Topology Builder emits — so the policy plugin reasons over the complete node,
// edge, and attachment detail the compiler holds rather than a narrow
// projection. PrecalculatedPaths carries the routing paths the analyzer
// enumerates alongside so policies can reason about reachability without
// re-walking the graph. The wire shape is single-sourced from
// ir-v1beta1.schema.json (referenced by wasm-policy-v1beta1.schema.json); the
// generated guest codec in aggrega-abi mirrors it.
//
// assemblePolicyInput populates the structure on every build; the Step-4
// policy socket encodes it as canonical CBOR for a configured policy plugin and
// reads the plugin's verdicts back. Graph is assembled whole here, and what crosses
// the socket is a copy of it with every leaf the graph's own sensitivity
// markings name replaced by the withheld marker — the same withholding the
// printed graph applies — so a plugin still reads where a datum an author
// declared sensitive sits and how it is marked, and never its value.
type PolicyInput struct {
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

// PolicySource is one definition file the host hands to a policy
// plugin: its base-name Path and the file's Content. The host reads the file at
// compile time and carries its bytes verbatim; it never parses the content and
// never exposes an absolute host path in Path.
type PolicySource struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// SeverityHalt, SeverityWarn, and SeverityInfo are the three severities a fired
// policy verdict may carry: halt stops the build, warn flags the verdict and lets
// the build proceed, info records the verdict without affecting the build
// outcome.
//
// They are re-exports of the generated [abi] consts, so the vocabulary has one
// home: the shared diagnostic schema declares the closed set, the generated
// Go/Rust/TypeScript codecs publish it to guests, and the compiler routes on the
// same three values a guest writes.
const (
	SeverityHalt = abi.SeverityHalt
	SeverityWarn = abi.SeverityWarn
	SeverityInfo = abi.SeverityInfo
)

// RenderPolicyInput is the payload the render stage hands to a
// policy socket that declares the render stage. Artifacts
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
type RenderPolicyInput struct {
	Artifacts []RenderedArtifact `json:"artifacts"`
	// Sources and Settings carry the same attachment-declared inputs as on the
	// IR-stage [PolicyInput]: the definition files read verbatim (paths
	// as base names, content opaque, sorted by path) and the free-form settings
	// object handed through untouched. Both are absent when the attachment
	// declares none.
	Sources  []PolicySource `json:"sources,omitempty"`
	Settings map[string]any `json:"settings,omitempty"`
}

// RenderedArtifact is one rendered output file in a render-stage policy input:
// its Path and the rendered Content at that path. Tree carries the parsed
// structured tree of the artifact when the host parsed it before handing it to
// the plugin — the canonical JSON view of a source format the policy guest does
// not read natively (an HCL artifact parsed to JSON, for example). It is absent
// when the host did not parse the artifact, in which case the plugin reads the
// native Content directly; the host carries the tree verbatim without
// interpreting its content.
type RenderedArtifact struct {
	Path    string          `json:"path"`
	Content string          `json:"content"`
	Tree    json.RawMessage `json:"tree,omitempty"`
	// Format is the rendered format when the producing provider declared it,
	// carried verbatim; absent when the provider declared no format.
	Format string `json:"format,omitempty"`
}
