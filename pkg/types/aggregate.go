// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

// Aggregate is the recursive, parameterized AI architecture — both the root application and reusable module.
// Schema: aggregate-v1alpha1.schema.json
type Aggregate struct {
	APIVersion string            `json:"apiVersion" yaml:"apiVersion"`
	Kind       string            `json:"kind" yaml:"kind"`
	Metadata   AggregateMetadata `json:"metadata" yaml:"metadata"`
	Defs       []string          `json:"defs,omitempty" yaml:"defs,omitempty"` // YAML anchor definitions for DRY
	Spec       AggregateSpec     `json:"spec" yaml:"spec"`
}

// AggregateMetadata carries identity, versioning, and optional metadata for an Aggregate.
type AggregateMetadata struct {
	Name        string       `json:"name" yaml:"name"`
	Version     string       `json:"version" yaml:"version"`
	Description string       `json:"description,omitempty" yaml:"description,omitempty"`
	Maintainers []Maintainer `json:"maintainers,omitempty" yaml:"maintainers,omitempty"`
	License     string       `json:"license,omitempty" yaml:"license,omitempty"`
	Repository  string       `json:"repository,omitempty" yaml:"repository,omitempty"`
	Home        string       `json:"home,omitempty" yaml:"home,omitempty"`
	Keywords    []string     `json:"keywords,omitempty" yaml:"keywords,omitempty"`
	Deprecated  bool         `json:"deprecated,omitempty" yaml:"deprecated,omitempty"`
	// Labels are module-level duck-typing labels for catalog discovery and policy evaluation.
	// They describe the Aggregate's identity as a reusable module and do NOT cascade to child
	// component nodes. Component labels come exclusively from the Component catalog entity.
	Labels OrderedMap[string] `json:"labels,omitempty,omitzero" yaml:"labels,omitempty"`
}

// AggregateSpec defines the components, sub-aggregates, subsystems, and
// parameters. Note the absence of a top-level Annotations field by design:
// per-instance Annotations live on [Subsystem]; Aggregate-level annotation
// cascading has no authoring surface, and the IR's per-node annotation
// buckets that derive from this entity are sourced from Subsystem.Annotations
// alone.
type AggregateSpec struct {
	Parameters         OrderedMap[interface{}]       `json:"parameters,omitempty,omitzero" yaml:"parameters,omitempty"`
	Aggregates         OrderedMap[SubAggregate]      `json:"aggregates,omitempty,omitzero" yaml:"aggregates,omitempty"`
	ExportedComponents OrderedMap[string]            `json:"exportedComponents,omitempty,omitzero" yaml:"exportedComponents,omitempty"`
	Subsystems         OrderedMap[Subsystem]         `json:"subsystems,omitempty,omitzero" yaml:"subsystems,omitempty"`
	Components         OrderedMap[ComponentInstance] `json:"components,omitempty,omitzero" yaml:"components,omitempty"`
}

// SubAggregate is a nested aggregate acting as an imported module.
// Schema: aggregate-v1alpha1.schema.json#/properties/spec/properties/aggregates
type SubAggregate struct {
	Type       string                  `json:"type" yaml:"type"`
	Parameters OrderedMap[interface{}] `json:"parameters,omitempty,omitzero" yaml:"parameters,omitempty"`
}

// Subsystem is a logical grouping of developer intent. Recursive via nested Subsystems.
// Schema: aggregate-v1alpha1.schema.json#/$defs/subsystem
type Subsystem struct {
	Description string                        `json:"description,omitempty" yaml:"description,omitempty"`
	Annotations OrderedMap[string]            `json:"annotations,omitempty,omitzero" yaml:"annotations,omitempty"`
	Features    OrderedMap[FeatureInstance]   `json:"features,omitempty,omitzero" yaml:"features,omitempty"`
	Subsystems  OrderedMap[Subsystem]         `json:"subsystems,omitempty,omitzero" yaml:"subsystems,omitempty"`
	Components  OrderedMap[ComponentInstance] `json:"components,omitempty,omitzero" yaml:"components,omitempty"`
}

// ComponentInstance is one logical node in the aggregate topology (Developer-authored).
// Schema: aggregate-v1alpha1.schema.json#/$defs/component_instance
type ComponentInstance struct {
	Type        string                      `json:"type" yaml:"type"`
	Description string                      `json:"description,omitempty" yaml:"description,omitempty"`
	Values      OrderedMap[interface{}]     `json:"values,omitempty,omitzero" yaml:"values,omitempty"`
	Inject      *InjectBlock                `json:"inject,omitempty" yaml:"inject,omitempty"` // Component-level self-inject
	Uses        OrderedMap[interface{}]     `json:"uses,omitempty,omitzero" yaml:"uses,omitempty"` // UsesBlock: socket → simple []string or verbose map
	Features    OrderedMap[FeatureInstance] `json:"features,omitempty,omitzero" yaml:"features,omitempty"`
	Secrets     OrderedMap[SecretInstance]  `json:"secrets,omitempty,omitzero" yaml:"secrets,omitempty"`
}
