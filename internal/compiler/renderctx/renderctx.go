// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderctx

import (
	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// RenderContext is the render-time context a Provider guest receives:
// the fulfilled parameters, the three annotation buckets
// ([RenderAnnotations]), the [BoundaryContext] the node renders within,
// and the render-wide indentation width the guest applies when
// serializing its output.
//
// RenderContext is the guest-facing wire projection of render-time
// context and is deliberately distinct from the package's host-internal
// NodeRenderContext: NodeRenderContext carries Go-side fields — resolved
// source paths, provenance — that exist to drive in-process rendering
// and are not part of the guest ABI. The two types serve different
// boundaries (one is the host's internal render argument, the other the
// cross-boundary wire shape) and are not interchangeable; do not unify
// them.
//
// Parameters preserves author order via types.OrderedMap so the wire
// payload stays deterministic. YAMLIndent is the render-wide indentation
// width the guest serializes output at; the zero value means "absent" and
// the guest falls back to its clamp default.
type RenderContext struct {
	Parameters  types.OrderedMap[any] `json:"parameters,omitempty,omitzero"`
	Annotations RenderAnnotations     `json:"annotations"`
	Boundary    BoundaryContext       `json:"boundary"`
	YAMLIndent  int                   `json:"yaml_indent,omitempty"`
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
// assembly output; the parser, importer, publishing-linter, policy, and
// feature-recipe-pipeline ABI surfaces carry this exact type rather than
// redeclaring their own, so a diagnostic carries one shape across every
// plugin boundary. It is an alias for the single generated
// [abi.Diagnostic]: the wire shape lives in one place and every surface
// resolves to it.
//
// This is intentionally distinct from the editor-protocol diagnostic the
// language server emits and from the tolerant-load diagnostic the
// lockfile loader emits: those serve different boundaries with different
// shapes and severity vocabularies, and unifying any of the three would
// couple unrelated surfaces. Diagnostic here is the Wasm-plugin ABI
// diagnostic and only that.
//
// Severity is one of the three values every socket shares — halt, warn,
// or info. Message is the human-readable text. Location, when present, pins the
// diagnostic to a source position via [DiagnosticLocation]. The
// socket-specific Code, RuleID, Classification, SourceRef, and Suggest
// fields are populated by the sockets that use them and omitted otherwise.
type Diagnostic = abi.Diagnostic

// DiagnosticLocation pins a [Diagnostic] to a source position. It is an
// alias for the single generated [abi.Location]: Span carries a raw-byte
// source span, KeyPath the dotted path into a structured document, URI
// the source artifact, Line a document line, and Kind the source surface.
type DiagnosticLocation = abi.Location
