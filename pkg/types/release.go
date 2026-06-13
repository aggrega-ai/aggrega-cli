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
// Secrets remains OrderedMap[any] — boundary-wide secret attachments
// still arrive as opaque payloads the IR builder coerces separately.
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
	Secrets     OrderedMap[any]               `json:"secrets,omitempty,omitzero" yaml:"secrets,omitempty"`
	Boundaries  OrderedMap[Boundary]          `json:"boundaries,omitempty,omitzero" yaml:"boundaries,omitempty"`
	Components  OrderedMap[BoundaryComponent] `json:"components,omitempty,omitzero" yaml:"components,omitempty"`
	Aggregates  OrderedMap[AggregateRouting]  `json:"aggregates,omitempty,omitzero" yaml:"aggregates,omitempty"`

	// Synthesized marks a boundary the compiler created rather than the
	// author — the `default` boundary synthesized for a Release that declares
	// top-level components instead of named boundaries. It is derived
	// provenance, never authored: the json/yaml `-` tags keep it out of the
	// authoring surface entirely, and it is set only by NormalizeReleaseScopes.
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
// metadata; the former is the actual sizing carrier for a routed
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
	Secrets     OrderedMap[BoundaryFeature]  `json:"secrets,omitempty,omitzero" yaml:"secrets,omitempty"`
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
// declared-but-unbound socket, pointing at an existing component) and/or Infra
// (per-edge operator config validated against the socket infraSchema). It has
// no Values field by construction — the Release values: ban holds at the edge
// surface. Retargeting a developer-declared edge is rejected at build (a Loud
// Guardrail), not represented here.
// Schema: release-v1alpha1.schema.json#/$defs/release_uses_block
type ReleaseUsesEntry struct {
	Target string          `json:"target,omitempty" yaml:"target,omitempty"`
	Infra  OrderedMap[any] `json:"infra,omitempty,omitzero" yaml:"infra,omitempty"`
}

// BoundaryFeature is a feature or secret attachment within a Release boundary.
// Release provides type (for Ops-injected), infra (physical sizing), and uses
// (topology wiring). Never values — values are the Developer's domain on the
// Aggregate.
//
// Uses on a boundary-wide attachment projects per-consumer: a boundary (or
// subsystem) feature carrying a uses-edge to a shared target contributes that
// edge from every component in the scope, so the "every component here depends
// on shared target D" pattern is declared once at the scope rather than
// repeated on each component. The edge carries the attachment as its origin
// feature, so a consumer is injected the target's provides exactly as it would
// be for a per-component feature uses-edge.
// Schema: release-v1alpha1.schema.json#/$defs/boundary_component/properties/features
type BoundaryFeature struct {
	Type        string                      `json:"type,omitempty" yaml:"type,omitempty"`
	Description string                      `json:"description,omitempty" yaml:"description,omitempty"`
	Infra       OrderedMap[any]             `json:"infra,omitempty,omitzero" yaml:"infra,omitempty"`
	Uses        OrderedMap[UsesSocketValue] `json:"uses,omitempty,omitzero" yaml:"uses,omitempty"`
}

// AggregateRouting routes a nested Aggregate sub-module's components into a boundary.
// Schema: release-v1alpha1.schema.json#/$defs/aggregate_routing
type AggregateRouting struct {
	Components OrderedMap[BoundaryComponent] `json:"components,omitempty,omitzero" yaml:"components,omitempty"`
	Aggregates OrderedMap[AggregateRouting]  `json:"aggregates,omitempty,omitzero" yaml:"aggregates,omitempty"`
}
