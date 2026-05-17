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
// parameters. Annotations live on per-instance carriers: [Subsystem] and
// [ComponentInstance] both carry an Annotations field for Developer-side
// authoring of annotation-driven Feature shortcuts. The IR's per-node
// annotation buckets that derive from this entity are sourced from those
// per-instance fields; AggregateSpec itself carries no top-level
// Annotations.
type AggregateSpec struct {
	Parameters         OrderedMap[any]               `json:"parameters,omitempty,omitzero" yaml:"parameters,omitempty"`
	Aggregates         OrderedMap[SubAggregate]      `json:"aggregates,omitempty,omitzero" yaml:"aggregates,omitempty"`
	ExportedComponents OrderedMap[string]            `json:"exportedComponents,omitempty,omitzero" yaml:"exportedComponents,omitempty"`
	Subsystems         OrderedMap[Subsystem]         `json:"subsystems,omitempty,omitzero" yaml:"subsystems,omitempty"`
	Components         OrderedMap[ComponentInstance] `json:"components,omitempty,omitzero" yaml:"components,omitempty"`
}

// SubAggregate is a nested aggregate acting as an imported module.
// Schema: aggregate-v1alpha1.schema.json#/properties/spec/properties/aggregates
type SubAggregate struct {
	Type       string          `json:"type" yaml:"type"`
	Parameters OrderedMap[any] `json:"parameters,omitempty,omitzero" yaml:"parameters,omitempty"`
}

// Subsystem is a logical grouping of developer intent. Recursive via nested Subsystems.
// Schema: aggregate-v1alpha1.schema.json#/$defs/subsystem
type Subsystem struct {
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// Annotations are Developer-authored annotations at the subsystem
	// scope. Each entry's value is one of three shapes encoded via
	// [AnnotationValue]:
	//
	//   - Scalar — a single string. The binding's Maps must declare
	//     exactly one path; the scalar routes to that path.
	//   - Structured — a map of subkey to subvalue. Each subkey routes
	//     through the binding's Maps to the Feature schema field with
	//     the same name; subkeys whose pillar is incompatible with the
	//     authoring side halt the build.
	//   - Nil pointer (explicit YAML null at the value site) — the
	//     subsystem opts out of the inherited binding wholesale for
	//     this scope.
	//
	// Structured values also support per-subkey null opt-out: a subkey
	// with a null value inside a Structured map suppresses the
	// corresponding inherited subkey while leaving sibling subkeys
	// untouched.
	//
	// Annotations declared here are inherited by nested subsystems and
	// by contained components; they do not propagate sideways to
	// sibling subsystems. The Aggregate-as-Authoring-Surface contract
	// permits routing only through the binding's values-pillar paths;
	// bindings whose maps route an annotation key (or a subkey of a
	// Structured value) through the infra pillar halt at annotation
	// expansion time.
	Annotations OrderedMap[*AnnotationValue] `json:"annotations,omitempty,omitzero" yaml:"annotations,omitempty"`

	Features   OrderedMap[FeatureInstance]   `json:"features,omitempty,omitzero" yaml:"features,omitempty"`
	Subsystems OrderedMap[Subsystem]         `json:"subsystems,omitempty,omitzero" yaml:"subsystems,omitempty"`
	Components OrderedMap[ComponentInstance] `json:"components,omitempty,omitzero" yaml:"components,omitempty"`
}

// ComponentInstance is one logical node in the aggregate topology (Developer-authored).
// Schema: aggregate-v1alpha1.schema.json#/$defs/component_instance
type ComponentInstance struct {
	Type        string                      `json:"type" yaml:"type"`
	Description string                      `json:"description,omitempty" yaml:"description,omitempty"`
	Values      OrderedMap[any]             `json:"values,omitempty,omitzero" yaml:"values,omitempty"`
	Inject      *InjectBlock                `json:"inject,omitempty" yaml:"inject,omitempty"`      // Component-level self-inject
	Uses        OrderedMap[UsesSocketValue] `json:"uses,omitempty,omitzero" yaml:"uses,omitempty"` // UsesBlock: socket → simple []string or verbose alias-map
	Features    OrderedMap[FeatureInstance] `json:"features,omitempty,omitzero" yaml:"features,omitempty"`
	Secrets     OrderedMap[SecretInstance]  `json:"secrets,omitempty,omitzero" yaml:"secrets,omitempty"`

	// Annotations are Developer-authored annotations at the component
	// scope. Values follow the same [AnnotationValue] shape as on
	// [Subsystem]: Scalar, Structured, or nil-pointer for whole-value
	// opt-out. Structured values additionally support per-subkey null
	// opt-out — a subkey with a null value inside a Structured map
	// suppresses the corresponding inherited subkey while leaving
	// sibling subkeys untouched.
	//
	// Component-level annotations declared here override inherited
	// subsystem-level annotations per key; sibling components in the
	// same subsystem retain their own annotation maps independently.
	// The Aggregate-as-Authoring-Surface contract permits routing only
	// through the binding's values-pillar paths; bindings whose maps
	// route an annotation key (or a subkey of a Structured value)
	// through the infra pillar halt at annotation expansion time.
	Annotations OrderedMap[*AnnotationValue] `json:"annotations,omitempty,omitzero" yaml:"annotations,omitempty"`
}
