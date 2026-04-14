// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

// Feature is a modular operational feature that binds dynamically to components.
// Schema: feature-v1alpha1.schema.json
type Feature struct {
	APIVersion string          `json:"apiVersion" yaml:"apiVersion"`
	Kind       string          `json:"kind" yaml:"kind"`
	Metadata   FeatureMetadata `json:"metadata" yaml:"metadata"`
	Spec       FeatureSpec     `json:"spec" yaml:"spec"`
}

// FeatureMetadata carries identity and optional labels for a Feature.
// The label feature.aggrega.ai/kind with value "secret" marks this as a credential Feature.
type FeatureMetadata struct {
	Name        string            `json:"name" yaml:"name"`
	Version     string            `json:"version" yaml:"version"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	Maintainers []Maintainer      `json:"maintainers,omitempty" yaml:"maintainers,omitempty"`
	License     string            `json:"license,omitempty" yaml:"license,omitempty"`
	Repository  string            `json:"repository,omitempty" yaml:"repository,omitempty"`
	Home        string            `json:"home,omitempty" yaml:"home,omitempty"`
	Keywords    []string          `json:"keywords,omitempty" yaml:"keywords,omitempty"`
	Deprecated  bool              `json:"deprecated,omitempty" yaml:"deprecated,omitempty"`
	Labels      map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

// FeatureSpec defines the validation, targeting, and interoperability of a catalog Feature.
type FeatureSpec struct {
	Extends              string                     `json:"extends,omitempty" yaml:"extends,omitempty"`
	SecretNames          []string                   `json:"secretNames,omitempty" yaml:"secretNames,omitempty"`
	AppliesTo            map[string]string          `json:"appliesTo,omitempty" yaml:"appliesTo,omitempty"`
	AppliesToExpressions []LabelSelectorRequirement `json:"appliesToExpressions,omitempty" yaml:"appliesToExpressions,omitempty"`
	AppliesToAnyOf       []SelectorTerm             `json:"appliesToAnyOf,omitempty" yaml:"appliesToAnyOf,omitempty"`
	Interoperability     *Interoperability          `json:"interoperability,omitempty" yaml:"interoperability,omitempty"`
	UsesSchema           map[string]UsesSchemaEntry `json:"usesSchema,omitempty" yaml:"usesSchema,omitempty"`
	CueSchema            string                     `json:"cueSchema,omitempty" yaml:"cueSchema,omitempty"`
	CueSchemaRef         string                     `json:"cueSchemaRef,omitempty" yaml:"cueSchemaRef,omitempty"`
}

// LabelSelectorRequirement is a single label selector requirement (AND logic).
// Schema: feature-v1alpha1.schema.json#/properties/spec/properties/appliesToExpressions/items
type LabelSelectorRequirement struct {
	Key      string   `json:"key" yaml:"key"`
	Operator string   `json:"operator" yaml:"operator"` // In, NotIn, Exists, DoesNotExist
	Values   []string `json:"values,omitempty" yaml:"values,omitempty"`
}

// SelectorTerm is one selector term in appliesToAnyOf (OR logic between terms, AND within).
// Schema: feature-v1alpha1.schema.json#/properties/spec/properties/appliesToAnyOf/items
type SelectorTerm struct {
	MatchLabels      map[string]string          `json:"matchLabels,omitempty" yaml:"matchLabels,omitempty"`
	MatchExpressions []LabelSelectorRequirement `json:"matchExpressions,omitempty" yaml:"matchExpressions,omitempty"`
}

// Interoperability defines operational safety and interoperability controls for a Feature.
type Interoperability struct {
	ConflictsWith    []string `json:"conflictsWith,omitempty" yaml:"conflictsWith,omitempty"`
	ComponentRefPath string   `json:"componentRefPath,omitempty" yaml:"componentRefPath,omitempty"`
}
