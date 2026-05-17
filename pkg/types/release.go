// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

// Release binds an Aggregate to a Manifest and routes components into recursive boundaries.
// Schema: release-v1alpha1.schema.json
type Release struct {
	APIVersion string          `json:"apiVersion" yaml:"apiVersion"`
	Kind       string          `json:"kind" yaml:"kind"`
	Metadata   ReleaseMetadata `json:"metadata" yaml:"metadata"`
	Spec       ReleaseSpec     `json:"spec" yaml:"spec"`
}

// ReleaseMetadata carries identity for a Release, including the target environment.
type ReleaseMetadata struct {
	Name        string       `json:"name" yaml:"name"`
	Environment string       `json:"environment" yaml:"environment"`
	Version     string       `json:"version" yaml:"version"`
	Description string       `json:"description,omitempty" yaml:"description,omitempty"`
	Maintainers []Maintainer `json:"maintainers,omitempty" yaml:"maintainers,omitempty"`
	License     string       `json:"license,omitempty" yaml:"license,omitempty"`
	Repository  string       `json:"repository,omitempty" yaml:"repository,omitempty"`
	Home        string       `json:"home,omitempty" yaml:"home,omitempty"`
	Keywords    []string     `json:"keywords,omitempty" yaml:"keywords,omitempty"`
	Deprecated  bool         `json:"deprecated,omitempty" yaml:"deprecated,omitempty"`
}

// ReleaseSpec defines the aggregate reference, manifest, parameters, and
// boundary routing. Boundaries' SyncWave fields carry per-boundary
// deployment-ordering integers; the IR builder reads each boundary's
// SyncWave and propagates it to every contained node. Boundaries is
// the only required field — every Release must declare at least one
// boundary, since a Release with no boundaries has nothing to route into.
type ReleaseSpec struct {
	Aggregate  string               `json:"aggregate,omitempty" yaml:"aggregate,omitempty"`
	Manifest   string               `json:"manifest,omitempty" yaml:"manifest,omitempty"`
	Extends    string               `json:"extends,omitempty" yaml:"extends,omitempty"`
	Parameters ParametersBlock      `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	Boundaries OrderedMap[Boundary] `json:"boundaries" yaml:"boundaries"`
}

// Boundary is a recursive logical box into which components are routed.
// Schema: release-v1alpha1.schema.json#/$defs/boundary
//
// The Features and Secrets fields are typed as OrderedMap[any]
// rather than OrderedMap[BoundaryFeature]: boundary-wide feature and
// secret attachments arrive from YAML as opaque payloads that the IR
// builder coerces into typed shapes via parseBoundaryFeature during
// Step 3, parallel to how Tier 4/5 of the IR's feature cascade collects
// boundary-wide attachments separately from per-component ones.
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
	Features    OrderedMap[any]               `json:"features,omitempty,omitzero" yaml:"features,omitempty"`
	Secrets     OrderedMap[any]               `json:"secrets,omitempty,omitzero" yaml:"secrets,omitempty"`
	Boundaries  OrderedMap[Boundary]          `json:"boundaries,omitempty,omitzero" yaml:"boundaries,omitempty"`
	Components  OrderedMap[BoundaryComponent] `json:"components,omitempty,omitzero" yaml:"components,omitempty"`
	Aggregates  OrderedMap[AggregateRouting]  `json:"aggregates,omitempty,omitzero" yaml:"aggregates,omitempty"`
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
	Infra       OrderedMap[any]             `json:"infra,omitempty,omitzero" yaml:"infra,omitempty"`
	Features    OrderedMap[BoundaryFeature] `json:"features,omitempty,omitzero" yaml:"features,omitempty"`
	Secrets     OrderedMap[BoundaryFeature] `json:"secrets,omitempty,omitzero" yaml:"secrets,omitempty"`
}

// BoundaryFeature is a feature or secret attachment within a Release boundary.
// Release provides only type (for Ops-injected) and infra (physical sizing). Never values.
// Schema: release-v1alpha1.schema.json#/$defs/boundary_component/properties/features
type BoundaryFeature struct {
	Type        string          `json:"type,omitempty" yaml:"type,omitempty"`
	Description string          `json:"description,omitempty" yaml:"description,omitempty"`
	Infra       OrderedMap[any] `json:"infra,omitempty,omitzero" yaml:"infra,omitempty"`
}

// AggregateRouting routes a nested Aggregate sub-module's components into a boundary.
// Schema: release-v1alpha1.schema.json#/$defs/aggregate_routing
type AggregateRouting struct {
	Components OrderedMap[BoundaryComponent] `json:"components,omitempty,omitzero" yaml:"components,omitempty"`
	Aggregates OrderedMap[AggregateRouting]  `json:"aggregates,omitempty,omitzero" yaml:"aggregates,omitempty"`
}
