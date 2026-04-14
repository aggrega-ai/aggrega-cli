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

// ReleaseSpec defines the aggregate reference, manifest, parameters, and boundary routing.
type ReleaseSpec struct {
	Aggregate      string                 `json:"aggregate,omitempty" yaml:"aggregate,omitempty"`
	Manifest       string                 `json:"manifest,omitempty" yaml:"manifest,omitempty"`
	Extends        string                 `json:"extends,omitempty" yaml:"extends,omitempty"`
	Parameters     map[string]interface{} `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	ParametersFrom []string               `json:"parametersFrom,omitempty" yaml:"parametersFrom,omitempty"`
	SyncWaves      map[string][]string    `json:"sync_waves,omitempty" yaml:"sync_waves,omitempty"`
	Boundaries     map[string]Boundary    `json:"boundaries" yaml:"boundaries"`
}

// Boundary is a recursive logical box into which components are routed.
// Schema: release-v1alpha1.schema.json#/$defs/boundary
type Boundary struct {
	Description  string                       `json:"description,omitempty" yaml:"description,omitempty"`
	Directory    string                       `json:"directory,omitempty" yaml:"directory,omitempty"`
	SubDirectory string                       `json:"sub_directory,omitempty" yaml:"sub_directory,omitempty"`
	Annotations  map[string]string            `json:"annotations,omitempty" yaml:"annotations,omitempty"`
	Features     map[string]interface{}       `json:"features,omitempty" yaml:"features,omitempty"`
	Secrets      map[string]interface{}       `json:"secrets,omitempty" yaml:"secrets,omitempty"`
	Boundaries   map[string]Boundary          `json:"boundaries,omitempty" yaml:"boundaries,omitempty"`
	Components   map[string]BoundaryComponent `json:"components,omitempty" yaml:"components,omitempty"`
	Aggregates   map[string]AggregateRouting  `json:"aggregates,omitempty" yaml:"aggregates,omitempty"`
}

// BoundaryComponent is a component instance routed into a boundary with optional sizing and feature attachments.
// Schema: release-v1alpha1.schema.json#/$defs/boundary_component
type BoundaryComponent struct {
	Description  string                     `json:"description,omitempty" yaml:"description,omitempty"`
	SubDirectory string                     `json:"sub_directory,omitempty" yaml:"sub_directory,omitempty"`
	Infra        map[string]interface{}     `json:"infra,omitempty" yaml:"infra,omitempty"`
	Features     map[string]BoundaryFeature `json:"features,omitempty" yaml:"features,omitempty"`
	Secrets      map[string]BoundaryFeature `json:"secrets,omitempty" yaml:"secrets,omitempty"`
}

// BoundaryFeature is a feature or secret attachment within a Release boundary.
// Release provides only type (for Ops-injected) and infra (physical sizing). Never values.
// Schema: release-v1alpha1.schema.json#/$defs/boundary_component/properties/features
type BoundaryFeature struct {
	Type        string                 `json:"type,omitempty" yaml:"type,omitempty"`
	Description string                 `json:"description,omitempty" yaml:"description,omitempty"`
	Infra       map[string]interface{} `json:"infra,omitempty" yaml:"infra,omitempty"`
}

// AggregateRouting routes a nested Aggregate sub-module's components into a boundary.
// Schema: release-v1alpha1.schema.json#/$defs/aggregate_routing
type AggregateRouting struct {
	Components map[string]BoundaryComponent `json:"components,omitempty" yaml:"components,omitempty"`
	Aggregates map[string]AggregateRouting  `json:"aggregates,omitempty" yaml:"aggregates,omitempty"`
}
