// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

//go:generate go run ../../internal/tools/typegen release_gen.go

// Release's struct declarations (Release, ReleaseMetadata, ReleaseSpec) are
// generated into release_gen.go from the published release-v1alpha1 schema;
// their godoc is the schema's descriptions. This hand-written companion holds
// the routing types the generated spec references — Boundary (recursive),
// BoundaryComponent, BoundaryFeature, ReleaseUsesEntry, AggregateRouting — and
// ParametersBlock (content-shape dispatch). Boundaries' SyncWave fields carry
// per-boundary deployment-ordering integers the IR builder propagates to every
// contained node; boundaries is the only required spec field.

// Boundary is a recursive logical box into which components are routed.
// Schema: release-v1alpha1.schema.json#/$defs/boundary
//
// Features is typed as OrderedMap[BoundaryFeature], the same typed
// shape as [BoundaryComponent.Features]: boundary-wide feature
// attachments carry type, infra, and uses, decoded directly from YAML.
// A credential is a Feature carrying the feature.aggrega.ai/kind: secret
// label, so Features is the boundary's whole attachment surface — there is
// no separate credential surface at this scope.
//
// Boundary carries no Infra field — physical sizing is per-component
// concern and lives on [BoundaryComponent] only.
type Boundary struct {
	Description  string `json:"description,omitempty" yaml:"description,omitempty"`
	Directory    string `json:"directory,omitempty" yaml:"directory,omitempty"`
	SubDirectory string `json:"subDirectory,omitempty" yaml:"subDirectory,omitempty"`
	SyncWave     int    `json:"syncWave,omitempty" yaml:"syncWave,omitempty"`
	// Annotations carries boundary-wide Ops annotations. Each entry's
	// value is one of three shapes encoded via [AnnotationValue]:
	// Scalar (a single string), Structured (a map of subkey to
	// subvalue with per-subkey routing through the binding's Maps),
	// or nil pointer (explicit YAML null at the value site — opts
	// out of the inherited binding wholesale for this boundary).
	// Structured values additionally support per-subkey null opt-out:
	// a subkey with a null value inside a Structured map suppresses
	// the corresponding inherited subkey while sibling subkeys stand.
	//
	// The cascade walker at pkg/resolver/boundary.go propagates the
	// tristate; the post-cascade flattener filters whole-value nils
	// and per-subkey nils so downstream expansion consumes a clean
	// OrderedMap[AnnotationValue]. Render-context template
	// substitution sites consume the Scalar-projection of the same
	// map via [ScalarOnly] (structured annotations are routing
	// instructions for Feature expansion and do not surface in
	// template substrate).
	Annotations OrderedMap[*AnnotationValue]  `json:"annotations,omitempty,omitzero" yaml:"annotations,omitempty"`
	Features    OrderedMap[BoundaryFeature]   `json:"features,omitempty,omitzero" yaml:"features,omitempty"`
	Boundaries  OrderedMap[Boundary]          `json:"boundaries,omitempty,omitzero" yaml:"boundaries,omitempty"`
	Components  OrderedMap[BoundaryComponent] `json:"components,omitempty,omitzero" yaml:"components,omitempty"`
	Aggregates  OrderedMap[AggregateRouting]  `json:"aggregates,omitempty,omitzero" yaml:"aggregates,omitempty"`

	// Synthesized marks a boundary the compiler created rather than the
	// author — the `default` boundary synthesized for a Release that declares
	// top-level components instead of named boundaries. It is derived
	// provenance, never authored: the json/yaml `-` tags keep it out of the
	// authoring surface entirely, and it is set only by NormalizeRelease.
	// Read at the output-directory site to route the boundary's artifacts to
	// dist/ flat, and carried onto the IR node so the synthesized default is
	// distinguishable from a boundary a user explicitly named "default".
	Synthesized bool `json:"-" yaml:"-"`
}

// BoundaryComponent is a component instance routed into a boundary with
// optional sizing and feature attachments. Schema:
// release-v1alpha1.schema.json#/$defs/boundary_component
//
// Infra carries per-component physical-sizing payloads (instance type,
// replica count, storage class, etc.) that flow into the IR's per-node
// infra_mapping. The field's presence here distinguishes BoundaryComponent
// from [Boundary]: the latter is a routing container with annotation-only
// metadata; the former is what actually holds the sizing for a routed
// component.
type BoundaryComponent struct {
	Description  string `json:"description,omitempty" yaml:"description,omitempty"`
	SubDirectory string `json:"subDirectory,omitempty" yaml:"subDirectory,omitempty"`
	// Annotations carries component-specific Ops annotations with the
	// same [AnnotationValue] shape as [Boundary.Annotations]: Scalar,
	// Structured (with per-subkey null opt-out), or nil-pointer for
	// whole-value opt-out from the inherited cascade. Component-level
	// entries override the inherited boundary cascade per key; sibling
	// components retain their own annotation maps independently.
	Annotations OrderedMap[*AnnotationValue] `json:"annotations,omitempty,omitzero" yaml:"annotations,omitempty"`
	Infra       OrderedMap[any]              `json:"infra,omitempty,omitzero" yaml:"infra,omitempty"`
	Features    OrderedMap[BoundaryFeature]  `json:"features,omitempty,omitzero" yaml:"features,omitempty"`
	// Uses is the operator edge surface, socket-keyed then alias-keyed (the
	// same topology shape as the Aggregate uses block). Within Architect-
	// declared sockets the Release may wire a declared-but-unbound socket to an
	// existing component (target) and/or supply per-edge infra. It carries no
	// values (the Release values: ban). Distinct from [BoundaryFeature.Uses],
	// which is the feature-attachment topology, not the component-edge surface.
	Uses OrderedMap[OrderedMap[ReleaseUsesEntry]] `json:"uses,omitempty,omitzero" yaml:"uses,omitempty"`
}

// ReleaseUsesEntry is one operator-authored per-edge connection within a
// [BoundaryComponent.Uses] socket. It may set Target (to create an edge into a
// declared-but-unbound socket, pointing at an existing component), Infra
// (per-edge operator config validated against the socket infraSchema) and
// Features (attachments filling the slots the socket declares). It has no
// Values field by construction — the Release values: ban holds at the edge
// surface. Retargeting a developer-declared edge is rejected at build (a Loud
// Guardrail), not represented here.
// Schema: release-v1alpha1.schema.json#/$defs/release_uses_block
type ReleaseUsesEntry struct {
	Target string          `json:"target,omitempty" yaml:"target,omitempty"`
	Infra  OrderedMap[any] `json:"infra,omitempty,omitzero" yaml:"infra,omitempty"`
	// Features carries the attachments the Release puts on this connection,
	// keyed by the slot each fills, in author order: the sizing for an
	// attachment the Aggregate also declares on the connection, or a whole
	// attachment only the Release makes. A name has to be a slot the socket
	// declares; the build refuses any other name when it loads.
	Features OrderedMap[ReleaseConnectionAttachment] `json:"features,omitempty,omitzero" yaml:"features,omitempty"`
}

// ReleaseConnectionAttachment is one attachment a Release connection entry
// carries. Release provides type (for an attachment only the Release makes),
// description, and infra (operational sizing). Never values — values are the
// Developer's domain on the Aggregate, and the Release schema refuses the key.
// Schema: release-v1alpha1.schema.json#/$defs/release_connection_attachment
type ReleaseConnectionAttachment struct {
	Type        string          `json:"type,omitempty" yaml:"type,omitempty"`
	Description string          `json:"description,omitempty" yaml:"description,omitempty"`
	Infra       OrderedMap[any] `json:"infra,omitempty,omitzero" yaml:"infra,omitempty"`
}

// BoundaryFeature is a feature attachment within a Release boundary — including
// a credential, which is a Feature carrying the feature.aggrega.ai/kind: secret
// label and attaches through the same surface as any other. Release provides
// type (for Ops-injected), infra (physical sizing), and uses (topology wiring).
// Never values — values are the Developer's domain on the Aggregate.
//
// Uses wires a topology edge at whichever breadth the attachment was declared,
// and the two breadths are one declaration rather than two features. Attached
// boundary-wide it projects per-consumer: a boundary (or subsystem) feature
// carrying a uses-edge to a shared target contributes that edge from every
// component in the scope, so the "every component here depends on shared target
// D" pattern is declared once at the scope rather than repeated on each
// component. Attached to a single boundary component it contributes the same
// edge from that one component. Either breadth stamps the attachment as the
// edge's origin feature, so a consumer is injected the target's provides
// exactly as it would be for a per-component feature uses-edge.
// Schema: release-v1alpha1.schema.json#/$defs/boundary_component/properties/features
type BoundaryFeature struct {
	Type        string                             `json:"type,omitempty" yaml:"type,omitempty"`
	Description string                             `json:"description,omitempty" yaml:"description,omitempty"`
	Infra       OrderedMap[any]                    `json:"infra,omitempty,omitzero" yaml:"infra,omitempty"`
	Uses        OrderedMap[FeatureUsesSocketValue] `json:"uses,omitempty,omitzero" yaml:"uses,omitempty"`
}

// AggregateRouting routes a nested Aggregate sub-module's components into a boundary.
// Schema: release-v1alpha1.schema.json#/$defs/aggregate_routing
type AggregateRouting struct {
	Components OrderedMap[BoundaryComponent] `json:"components,omitempty,omitzero" yaml:"components,omitempty"`
	Aggregates OrderedMap[AggregateRouting]  `json:"aggregates,omitempty,omitzero" yaml:"aggregates,omitempty"`
}

// NormalizedRelease wraps a [Release] that has passed [NormalizeRelease] — its
// top-level components folded into the synthesized `default` boundary, so the
// root component map is empty and every component lives under a boundary. The
// unexported field plus the sole constructor make "normalized" a compile-time
// invariant: a function taking a NormalizedRelease cannot be handed a raw,
// un-normalized Release. Symmetric to [NormalizedAggregate].
type NormalizedRelease struct {
	rel *Release
}

// NormalizeRelease is the SOLE constructor for [NormalizedRelease]. When the
// Release declares top-level components and no named boundaries, it synthesizes
// one boundary named "default" holding those components, inserted first (so
// emission/flatten order is unchanged), marked [Boundary.Synthesized], and
// clears the top-level components — they now live in the boundary. The mutual
// exclusivity of components and boundaries is enforced upstream at the
// document-envelope gate; this runs only on the components-only shape and is a
// no-op for a boundaries-bearing or empty Release. Idempotent.
//
// NON-MUTATING: when synthesis is needed it operates on a shallow copy of the
// Release and wraps the copy, leaving the caller's *Release untouched — a sole
// constructor that gutted its input would make every caller still holding the
// original a latent aliasing bug. Only Spec.Boundaries and Spec.Components are
// reassigned on the copy (the moved components map is shared read-only into the
// synthesized default), so iteration order is preserved and determinism holds.
func NormalizeRelease(rel *Release) NormalizedRelease {
	if rel == nil || rel.Spec.Boundaries.Len() > 0 || rel.Spec.Components.Len() == 0 {
		return NormalizedRelease{rel: rel}
	}
	cp := *rel
	def := Boundary{Components: rel.Spec.Components, Synthesized: true}
	var boundaries OrderedMap[Boundary]
	boundaries.Set("default", def)
	cp.Spec.Boundaries = boundaries
	cp.Spec.Components = OrderedMap[BoundaryComponent]{}
	return NormalizedRelease{rel: &cp}
}

// Release returns the underlying normalized Release for read access. The
// returned pointer is the normalized value (root folded into `default`); it is
// exposed for field reads, not for re-wrapping a mutated copy as normalized.
func (n NormalizedRelease) Release() *Release { return n.rel }
