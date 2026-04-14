// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

// Aggregate is the recursive, parameterized AI architecture — both the root application and reusable module.
// Schema: aggregate-v1alpha1.schema.json
type Aggregate struct {
	APIVersion string            `json:"apiVersion" yaml:"apiVersion"`
	Kind       string            `json:"kind" yaml:"kind"`
	Metadata   AggregateMetadata `json:"metadata" yaml:"metadata"`
	Spec       AggregateSpec     `json:"spec" yaml:"spec"`
}

// AggregateMetadata carries identity, versioning, and optional metadata for an Aggregate.
type AggregateMetadata struct {
	Name        string            `json:"name" yaml:"name"`
	Version     string            `json:"version" yaml:"version"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	Maintainers []Maintainer      `json:"maintainers,omitempty" yaml:"maintainers,omitempty"`
	License     string            `json:"license,omitempty" yaml:"license,omitempty"`
	Repository  string            `json:"repository,omitempty" yaml:"repository,omitempty"`
	Home        string            `json:"home,omitempty" yaml:"home,omitempty"`
	Keywords    []string          `json:"keywords,omitempty" yaml:"keywords,omitempty"`
	Deprecated bool              `json:"deprecated,omitempty" yaml:"deprecated,omitempty"`
	// Labels are module-level duck-typing labels for catalog discovery and policy evaluation.
	// They describe the Aggregate's identity as a reusable module and do NOT cascade to child
	// component nodes. Component labels come exclusively from the Component catalog entity.
	Labels map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
}

// AggregateSpec defines the components, sub-aggregates, subsystems, and parameters.
type AggregateSpec struct {
	Parameters         map[string]interface{}       `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	Aggregates         map[string]SubAggregate      `json:"aggregates,omitempty" yaml:"aggregates,omitempty"`
	ExportedComponents map[string]string            `json:"exported_components,omitempty" yaml:"exported_components,omitempty"`
	Subsystems         map[string]Subsystem         `json:"subsystems,omitempty" yaml:"subsystems,omitempty"`
	Components         map[string]ComponentInstance `json:"components,omitempty" yaml:"components,omitempty"`
}

// SubAggregate is a nested aggregate acting as an imported module.
// Schema: aggregate-v1alpha1.schema.json#/properties/spec/properties/aggregates
type SubAggregate struct {
	Type       string                 `json:"type" yaml:"type"`
	Parameters map[string]interface{} `json:"parameters,omitempty" yaml:"parameters,omitempty"`
}

// Subsystem is a logical grouping of developer intent. Recursive via nested Subsystems.
// Schema: aggregate-v1alpha1.schema.json#/$defs/subsystem
type Subsystem struct {
	Description string                       `json:"description,omitempty" yaml:"description,omitempty"`
	Annotations map[string]string            `json:"annotations,omitempty" yaml:"annotations,omitempty"`
	Features    map[string]FeatureInstance   `json:"features,omitempty" yaml:"features,omitempty"`
	Subsystems  map[string]Subsystem         `json:"subsystems,omitempty" yaml:"subsystems,omitempty"`
	Components  map[string]ComponentInstance `json:"components,omitempty" yaml:"components,omitempty"`
}

// ComponentInstance is one logical node in the aggregate topology (Developer-authored).
// Schema: aggregate-v1alpha1.schema.json#/$defs/component_instance
type ComponentInstance struct {
	Type        string                     `json:"type" yaml:"type"`
	Description string                     `json:"description,omitempty" yaml:"description,omitempty"`
	Values      map[string]interface{}     `json:"values,omitempty" yaml:"values,omitempty"`
	Uses        map[string]interface{}     `json:"uses,omitempty" yaml:"uses,omitempty"` // UsesBlock: socket → simple []string or verbose map
	Features    map[string]FeatureInstance `json:"features,omitempty" yaml:"features,omitempty"`
	Secrets     map[string]SecretInstance  `json:"secrets,omitempty" yaml:"secrets,omitempty"`
}
