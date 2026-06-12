// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

//go:generate go run ../../internal/tools/typegen aggregate_gen.go

// Aggregate's struct declarations (Aggregate, AggregateMetadata, AggregateSpec)
// are generated into aggregate_gen.go from the published aggregate-v1alpha1
// schema; their godoc is the schema's descriptions. This hand-written companion
// holds the per-instance carrier types the generated spec references —
// SubAggregate, Subsystem, and ComponentInstance — which carry the
// Annotations / Uses / Inject / Secrets surfaces and their polymorphic decode.
// Annotations live on those per-instance carriers; AggregateSpec itself carries
// no top-level Annotations.

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
