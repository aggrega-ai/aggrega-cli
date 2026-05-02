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
// boundary routing. SyncWaves carries the wave-keyed deployment-ordering
// map (wave name → list of node IDs); the IR builder reads this map and
// projects a per-node sync_wave field via reverse lookup. Boundaries is
// the only required field — every Release must declare at least one
// boundary, since a Release with no boundaries has nothing to route into.
type ReleaseSpec struct {
	Aggregate      string                    `json:"aggregate,omitempty" yaml:"aggregate,omitempty"`
	Manifest       string                    `json:"manifest,omitempty" yaml:"manifest,omitempty"`
	Extends        string                    `json:"extends,omitempty" yaml:"extends,omitempty"`
	Parameters     OrderedMap[interface{}]   `json:"parameters,omitempty,omitzero" yaml:"parameters,omitempty"`
	ParametersFrom []string                  `json:"parametersFrom,omitempty" yaml:"parametersFrom,omitempty"`
	SyncWaves      OrderedMap[[]string]      `json:"syncWaves,omitempty,omitzero" yaml:"syncWaves,omitempty"`
	Boundaries     OrderedMap[Boundary]      `json:"boundaries" yaml:"boundaries"`
}

// Boundary is a recursive logical box into which components are routed.
// Schema: release-v1alpha1.schema.json#/$defs/boundary
//
// The Features and Secrets fields are typed as OrderedMap[interface{}]
// rather than OrderedMap[BoundaryFeature]: boundary-wide feature and
// secret attachments arrive from YAML as opaque payloads that the IR
// builder coerces into typed shapes via parseBoundaryFeature during
// Step 3, parallel to how Tier 4/5 of the IR's feature cascade collects
// boundary-wide attachments separately from per-component ones.
//
// Boundary carries no Infra field — physical sizing is per-component
// concern and lives on [BoundaryComponent] only.
type Boundary struct {
	Description  string                         `json:"description,omitempty" yaml:"description,omitempty"`
	Directory    string                         `json:"directory,omitempty" yaml:"directory,omitempty"`
	SubDirectory string                         `json:"subDirectory,omitempty" yaml:"subDirectory,omitempty"`
	Annotations  OrderedMap[string]             `json:"annotations,omitempty,omitzero" yaml:"annotations,omitempty"`
	Features     OrderedMap[interface{}]        `json:"features,omitempty,omitzero" yaml:"features,omitempty"`
	Secrets      OrderedMap[interface{}]        `json:"secrets,omitempty,omitzero" yaml:"secrets,omitempty"`
	Boundaries   OrderedMap[Boundary]           `json:"boundaries,omitempty,omitzero" yaml:"boundaries,omitempty"`
	Components   OrderedMap[BoundaryComponent]  `json:"components,omitempty,omitzero" yaml:"components,omitempty"`
	Aggregates   OrderedMap[AggregateRouting]   `json:"aggregates,omitempty,omitzero" yaml:"aggregates,omitempty"`
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
	Description  string                       `json:"description,omitempty" yaml:"description,omitempty"`
	SubDirectory string                       `json:"subDirectory,omitempty" yaml:"subDirectory,omitempty"`
	Annotations  OrderedMap[string]           `json:"annotations,omitempty,omitzero" yaml:"annotations,omitempty"`
	Infra        OrderedMap[interface{}]      `json:"infra,omitempty,omitzero" yaml:"infra,omitempty"`
	Features     OrderedMap[BoundaryFeature]  `json:"features,omitempty,omitzero" yaml:"features,omitempty"`
	Secrets      OrderedMap[BoundaryFeature]  `json:"secrets,omitempty,omitzero" yaml:"secrets,omitempty"`
}

// BoundaryFeature is a feature or secret attachment within a Release boundary.
// Release provides only type (for Ops-injected) and infra (physical sizing). Never values.
// Schema: release-v1alpha1.schema.json#/$defs/boundary_component/properties/features
type BoundaryFeature struct {
	Type        string                  `json:"type,omitempty" yaml:"type,omitempty"`
	Description string                  `json:"description,omitempty" yaml:"description,omitempty"`
	Infra       OrderedMap[interface{}] `json:"infra,omitempty,omitzero" yaml:"infra,omitempty"`
}

// AggregateRouting routes a nested Aggregate sub-module's components into a boundary.
// Schema: release-v1alpha1.schema.json#/$defs/aggregate_routing
type AggregateRouting struct {
	Components OrderedMap[BoundaryComponent] `json:"components,omitempty,omitzero" yaml:"components,omitempty"`
	Aggregates OrderedMap[AggregateRouting]  `json:"aggregates,omitempty,omitzero" yaml:"aggregates,omitempty"`
}
