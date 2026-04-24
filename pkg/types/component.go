// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

// Component is the abstract definition of an atomic building block (catalog entity).
// Schema: component-v1alpha1.schema.json
type Component struct {
	APIVersion string            `json:"apiVersion" yaml:"apiVersion"`
	Kind       string            `json:"kind" yaml:"kind"`
	Metadata   ComponentMetadata `json:"metadata" yaml:"metadata"`
	Spec       ComponentSpec     `json:"spec" yaml:"spec"`
}

// ComponentMetadata carries identity and duck-typing labels for a Component.
// The labels field is required and must include the component.aggrega.ai/type key.
type ComponentMetadata struct {
	Name        string             `json:"name" yaml:"name"`
	Version     string             `json:"version" yaml:"version"`
	Description string             `json:"description,omitempty" yaml:"description,omitempty"`
	Maintainers []Maintainer       `json:"maintainers,omitempty" yaml:"maintainers,omitempty"`
	License     string             `json:"license,omitempty" yaml:"license,omitempty"`
	Repository  string             `json:"repository,omitempty" yaml:"repository,omitempty"`
	Home        string             `json:"home,omitempty" yaml:"home,omitempty"`
	Keywords    []string           `json:"keywords,omitempty" yaml:"keywords,omitempty"`
	Deprecated  bool               `json:"deprecated,omitempty" yaml:"deprecated,omitempty"`
	Labels      OrderedMap[string] `json:"labels" yaml:"labels"`
	Annotations OrderedMap[string] `json:"annotations,omitempty,omitzero" yaml:"annotations,omitempty"`
}

// ComponentSpec defines validation schemas and constraints for a catalog Component.
type ComponentSpec struct {
	Extends          string                      `json:"extends,omitempty" yaml:"extends,omitempty"`
	SecretNames      []string                    `json:"secretNames,omitempty" yaml:"secretNames,omitempty"` // Deprecated: use SecretsSchema. Kept as a fallback during transition.
	SecretsSchema    OrderedMap[SecretSlot]      `json:"secretsSchema,omitempty,omitzero" yaml:"secretsSchema,omitempty"`
	RequiredFeatures []string                    `json:"requiredFeatures,omitempty" yaml:"requiredFeatures,omitempty"`
	Inject           []string                    `json:"inject,omitempty" yaml:"inject,omitempty"` // Allowlist of allowed self-inject mechanisms (e.g., ["env", "file"]). Omission = Black-Box (no self-inject).
	UsesSchema       OrderedMap[UsesSchemaEntry] `json:"usesSchema,omitempty,omitzero" yaml:"usesSchema,omitempty"`
	CueSchema        string                      `json:"cueSchema,omitempty" yaml:"cueSchema,omitempty"`
	CueSchemaRef     string                      `json:"cueSchemaRef,omitempty" yaml:"cueSchemaRef,omitempty"`
}

// UsesSchemaEntry defines one socket category in a Component or Feature usesSchema.
// Schema: component-v1alpha1.schema.json#/properties/spec/properties/usesSchema
type UsesSchemaEntry struct {
	Selector string   `json:"selector" yaml:"selector"`
	Required bool     `json:"required,omitempty" yaml:"required,omitempty"`
	Inject   []string `json:"inject,omitempty" yaml:"inject,omitempty"`
}

// SecretSlot declares one credential slot in a Component or Feature secretsSchema.
// Required=true means the slot must be populated in the Aggregate. Inject is a
// allowlist of delivery mechanisms for this secret — empty means Black-Box
// (the secret is consumed by a Recipe internally, not delivered to the workload).
// Schema: component-v1alpha1.schema.json#/properties/spec/properties/secretsSchema
type SecretSlot struct {
	Required    bool     `json:"required,omitempty" yaml:"required,omitempty"`
	Inject      []string `json:"inject,omitempty" yaml:"inject,omitempty"`
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
}
