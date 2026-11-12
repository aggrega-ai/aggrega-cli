// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderctx

import (
	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// RenderContext is the render-time context a guest receives for a node:
// the fulfilled parameters, the three annotation buckets
// ([RenderAnnotations]), the [BoundaryContext] the node renders within,
// and, on a Provider export, the render-wide indentation width the guest
// applies when serializing its output. A Provider guest receives it on
// every export that renders or checks a node, and a pipeline step on its
// step input; both are built by one constructor in the renderer from the
// same IR node, so the two sockets see the same values for the same node.
// A boundary-scope FeatureRecipe, which has no node of its own, is handed
// one context on both sockets too: the boundary's, whose release bucket is
// the boundary's own annotation set and whose aggregate bucket is empty, so a
// guest reading the platform annotations ([RenderAnnotations.PlatformAnnotations])
// renders it by the boundary rather than by whichever component's turn
// dispatched it. The assembly export, which
// merges several nodes, carries only the indentation width: each
// contributor's annotations ride its own entry.
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

// PlatformAnnotations returns the one annotation set the platform annotations
// of a render are read from — the namespace, the image registry and every other
// aggrega.ai/ key a provider applies or a placement decision reads: the aggregate
// bucket with the release bucket written over it, so a key both of them carry
// takes the Release's value and a key only one of them carries takes that one's.
// The aggregate bucket already layers the authoring side within itself — a
// subsystem's annotations beneath a nested subsystem's, beneath the component
// instance's own, each read from the document declaring the component — so this
// is the last layer, and the Release is the last writer.
//
// The catalog bucket takes no part: a catalog entity's annotations describe the
// entity, and they never decide where one of its instances is placed.
//
// Keys come in author order, the aggregate bucket's first and then the keys only
// the Release writes, so a reader walking the set in order reads the same
// sequence for the same input. Where the aggregate bucket is empty the result
// holds exactly the release bucket's keys in its order.
//
// This is the one home of the layering: the host reads it for the placement it
// decides itself and for what it hands a provider per node, and a provider guest
// reads it for the keys it applies, so the two can never read a node's namespace
// from different sets.
func (a RenderAnnotations) PlatformAnnotations() types.OrderedMap[string] {
	var out types.OrderedMap[string]
	for k, v := range a.Aggregate.All() {
		out.Set(k, v)
	}
	for k, v := range a.Release.All() {
		out.Set(k, v)
	}
	return out
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
// the source artifact, Line a document line, Kind the source surface, and
// Instance the instance of the build the diagnostic concerns — never URI,
// which names a document.
type DiagnosticLocation = abi.Location
