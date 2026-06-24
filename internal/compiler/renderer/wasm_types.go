// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// RenderNodeInput is the JSON payload a Provider Wasm guest receives on
// its render_node export. It carries the per-node IR projection the
// guest renders ([IRNodeProjection]), the render-time [RenderContext],
// and an optional pre-packaged [CatalogProjection].
//
// When CatalogProjection is present, the guest renders from the payload
// alone and needs no host round-trips for catalog content. When it is
// omitted, the guest fetches catalog entries through the read-only host
// functions bound at load time.
//
// **Status: contract definition only — not yet executed.** This file
// ships the JSON ABI a Provider Wasm guest binds against; no production
// caller in this repository constructs or consumes the payload today.
// The runtime that invokes the guest and the reference Provider plugin
// that renders against this shape land in later work. The shape is
// stable surface so that work can bind against it now.
//
// Render-time AOT-purity contract: a guest renders deterministically
// from this payload and the read-only host functions alone. It performs
// no network access, no clock or random reads, and no filesystem or
// environment access. Given identical input it produces identical
// output — the determinism contract every downstream GitOps diff relies
// on.
type RenderNodeInput struct {
	Node              IRNodeProjection   `json:"node"`
	Context           RenderContext      `json:"context"`
	CatalogProjection *CatalogProjection `json:"catalog_projection,omitempty"`
}

// IRNodeProjection is the per-node IR data a Provider guest renders. It
// is a bounded projection of the full in-tree IR node — id, type,
// resolved recipe, values, feature attachments, and the node's outbound
// uses edges — narrower than the complete node surface, which also
// carries provenance, subsystem path, and source-file metadata the
// guest does not need to render. Constraining the surface matches the
// principle-of-least-authority discipline that applies to every Wasm
// plugin socket.
//
// Values, Features, and Uses preserve author order: they are
// types.OrderedMap so the JSON wire payload is byte-identical across
// runs of identical input and reflects the authored ordering rather
// than an alphabetical sort. Features reuses [types.IRFeatureEntry] —
// the same per-node feature-attachment shape the IR carries — so the
// projection never drifts behind the IR's feature entry.
type IRNodeProjection struct {
	ID             string                                 `json:"id"`
	Type           string                                 `json:"type"`
	ResolvedRecipe string                                 `json:"resolved_recipe"`
	Values         types.OrderedMap[any]                  `json:"values,omitempty,omitzero"`
	Features       types.OrderedMap[types.IRFeatureEntry] `json:"features,omitempty,omitzero"`
	Uses           types.OrderedMap[[]string]             `json:"uses,omitempty,omitzero"`
}

// RenderContext is the render-time context a Provider guest receives.
// It is a type alias for [renderctx.RenderContext]: the type lives in the
// leaf renderctx package so the feature-recipe-pipeline ABI surface can
// share it without importing the renderer, and the renderer re-exports it
// under its original name here for the in-package render call sites.
type RenderContext = renderctx.RenderContext

// RenderAnnotations carries the three annotation buckets a guest reads.
// It is a type alias for [renderctx.RenderAnnotations].
type RenderAnnotations = renderctx.RenderAnnotations

// BoundaryContext is the per-render boundary metadata a guest receives.
// It is a type alias for [renderctx.BoundaryContext].
type BoundaryContext = renderctx.BoundaryContext

// CatalogProjection is the pre-packaged catalog content a Provider guest
// renders against when the host supplies catalog entries in the payload
// rather than behind host functions. It is a closure walk outward from
// the IR node: only the catalog entries transitively referenced by the
// node being rendered are included, not the whole catalog.
//
// Each map is keyed by catalog URI. The maps are types.OrderedMap so the
// wire payload is byte-identical across runs of identical input,
// matching the deterministic-output contract the rest of the pipeline
// upholds.
type CatalogProjection struct {
	Components       types.OrderedMap[types.Component]      `json:"components,omitempty,omitzero"`
	Features         types.OrderedMap[types.Feature]        `json:"features,omitempty,omitzero"`
	Recipes          types.OrderedMap[types.Recipe]         `json:"recipes,omitempty,omitzero"`
	FeatureRecipes   types.OrderedMap[types.FeatureRecipe]  `json:"feature_recipes,omitempty,omitzero"`
	Providers        types.OrderedMap[types.Provider]       `json:"providers,omitempty,omitzero"`
	MappingLibraries types.OrderedMap[types.MappingLibrary] `json:"mapping_libraries,omitempty,omitzero"`
}

// RenderNodeOutput is the JSON payload a Provider Wasm guest returns
// from its render_node export: the rendered Content, a [RenderMetadata]
// sidecar, and any [Diagnostic] the guest raised while rendering.
//
// Content is provider-native opaque output — the guest decides its
// shape (a chart manifest object, a compose fragment, raw file bytes),
// and the host pipes it through without interpreting it.
type RenderNodeOutput struct {
	Content     any            `json:"content"`
	Metadata    RenderMetadata `json:"metadata"`
	Diagnostics []Diagnostic   `json:"diagnostics,omitempty"`
}

// RenderMetadata is the sidecar a guest returns alongside its rendered
// content.
//
// OutputFilenameHint suggests the filename the host should write the
// content to.
//
// PostProcessing names the idempotent post-render transforms the guest
// declares as safe to apply to its content (for example, sorting object
// keys or stripping nil fields). It is a declaration the host honors
// when it applies post-render transforms; the host applies only the
// transforms named here. The host-side transform step that reads this
// field lands with the runtime that consumes the output.
type RenderMetadata struct {
	OutputFilenameHint string   `json:"output_filename_hint,omitempty"`
	PostProcessing     []string `json:"post_processing,omitempty"`
}

// AssembleBoundaryInput is the JSON payload a Provider Wasm guest
// receives on its assemble_boundary export — the export that merges the
// per-node render outputs at one boundary into a single assembled
// artifact (an umbrella chart, a merged compose file). It carries the
// [BoundaryContext], the per-node [RenderedNode] outputs that fed the
// boundary, and the render-time [RenderContext].
//
// This is the cross-boundary wire shape of the per-boundary assembly the
// in-tree BoundaryAssembler performs today: a guest that implements
// assemble_boundary is the Wasm analog of that assembler. Not every
// provider declares the export — providers that emit only flat per-node
// output omit it, exactly as the in-tree assembler is optional.
type AssembleBoundaryInput struct {
	Boundary      BoundaryContext  `json:"boundary"`
	RenderedNodes []RenderedNode   `json:"rendered_nodes"`
	Context       RenderContext    `json:"context"`
}

// RenderedNode is one node's render output as it appears in an
// [AssembleBoundaryInput]: the node id, its type, and the
// [RenderNodeOutput] the render_node export produced for it.
type RenderedNode struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Rendered RenderNodeOutput `json:"rendered"`
}

// AssembleBoundaryOutput is the JSON payload a Provider guest returns
// from assemble_boundary. It is the same shape as [RenderNodeOutput] —
// assembled content plus the metadata sidecar plus diagnostics — and is
// declared as a type alias so the two output surfaces stay a single
// type. A future change that forks this into a separate declaration
// breaks the alias intentionally.
type AssembleBoundaryOutput = RenderNodeOutput

// Diagnostic is the shared diagnostic a Wasm guest raises across the
// plugin ABI surfaces. It is a type alias for [renderctx.Diagnostic]: the
// type lives in the leaf renderctx package so the linter and
// feature-recipe-pipeline ABI surfaces can import this exact type without
// importing the renderer, and the renderer re-exports it under its
// original name here.
type Diagnostic = renderctx.Diagnostic

// DiagnosticLocation pins a [Diagnostic] to a source position. It is a
// type alias for [renderctx.DiagnosticLocation].
type DiagnosticLocation = renderctx.DiagnosticLocation
