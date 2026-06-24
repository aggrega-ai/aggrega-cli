// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderctx

import "github.com/aggrega-ai/aggrega-cli/pkg/types"

// RenderContext is the render-time context a Provider guest receives:
// the fulfilled parameters, the three annotation buckets
// ([RenderAnnotations]), and the [BoundaryContext] the node renders
// within.
//
// RenderContext is the guest-facing wire projection of render-time
// context and is deliberately distinct from the package's host-internal
// NodeRenderContext: NodeRenderContext carries Go-side fields — resolved
// source paths, provenance, indentation width — that exist to drive
// in-process rendering and are not part of the guest ABI. The two types
// serve different boundaries (one is the host's internal render
// argument, the other the cross-boundary wire shape) and are not
// interchangeable; do not unify them.
//
// Parameters preserves author order via types.OrderedMap so the wire
// payload stays deterministic.
type RenderContext struct {
	Parameters  types.OrderedMap[any] `json:"parameters,omitempty,omitzero"`
	Annotations RenderAnnotations     `json:"annotations"`
	Boundary    BoundaryContext       `json:"boundary"`
}

// RenderAnnotations carries the three annotation buckets a guest reads —
// the annotations authored on the Aggregate, the annotations contributed
// by the Release, and the annotations declared in the catalog. Each
// bucket is an ordered map so the wire payload preserves author order
// and stays byte-identical across runs.
type RenderAnnotations struct {
	Aggregate types.OrderedMap[string] `json:"aggregate,omitempty,omitzero"`
	Release   types.OrderedMap[string] `json:"release,omitempty,omitzero"`
	Catalog   types.OrderedMap[string] `json:"catalog,omitempty,omitzero"`
}

// BoundaryContext is the per-render boundary metadata a guest receives:
// the boundary name, its path segments, the deployment sync wave, the
// output directory and optional sub-directory, and the boundary's own
// annotations. Annotations is ordered so the wire payload preserves
// author order.
type BoundaryContext struct {
	Name         string                   `json:"name"`
	Path         []string                 `json:"path"`
	SyncWave     int                      `json:"sync_wave,omitempty"`
	Directory    string                   `json:"directory"`
	SubDirectory string                   `json:"sub_directory,omitempty"`
	Annotations  types.OrderedMap[string] `json:"annotations,omitempty,omitzero"`
}

// Diagnostic is the shared diagnostic a Wasm guest raises across the
// plugin ABI surfaces. A Provider guest reports it in render and
// assembly output; the linter and feature-recipe-pipeline ABI surfaces
// import this exact type rather than redeclaring their own, so a
// diagnostic carries one shape across every plugin boundary.
//
// This is intentionally distinct from the editor-protocol diagnostic the
// language server emits and from the tolerant-load diagnostic the
// lockfile loader emits: those serve different boundaries with different
// shapes and severity vocabularies, and unifying any of the three would
// couple unrelated surfaces. Diagnostic here is the Wasm-plugin ABI
// diagnostic and only that.
//
// Severity is "error", "warning", or "info". Code is a stable
// machine-readable identifier for the diagnostic class. Message is the
// human-readable text. Location, when present, pins the diagnostic to a
// source position via [DiagnosticLocation]. Suggest, when present,
// offers a remediation hint.
type Diagnostic struct {
	Severity string              `json:"severity"`
	Code     string              `json:"code"`
	Message  string              `json:"message"`
	Location *DiagnosticLocation `json:"location,omitempty"`
	Suggest  string              `json:"suggest,omitempty"`
}

// DiagnosticLocation pins a [Diagnostic] to a source position. Kind
// names the source surface, URI identifies the source artifact, KeyPath
// is the dotted path to the offending key within it, and Line is the
// line number when one is known.
type DiagnosticLocation struct {
	Kind    string `json:"kind"`
	URI     string `json:"uri"`
	KeyPath string `json:"key_path,omitempty"`
	Line    int    `json:"line,omitempty"`
}
