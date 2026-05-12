// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import "gopkg.in/yaml.v3"

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
	Name        string             `json:"name" yaml:"name"`
	Version     string             `json:"version" yaml:"version"`
	Description string             `json:"description,omitempty" yaml:"description,omitempty"`
	Maintainers []Maintainer       `json:"maintainers,omitempty" yaml:"maintainers,omitempty"`
	License     string             `json:"license,omitempty" yaml:"license,omitempty"`
	Repository  string             `json:"repository,omitempty" yaml:"repository,omitempty"`
	Home        string             `json:"home,omitempty" yaml:"home,omitempty"`
	Keywords    []string           `json:"keywords,omitempty" yaml:"keywords,omitempty"`
	Deprecated  bool               `json:"deprecated,omitempty" yaml:"deprecated,omitempty"`
	Labels      OrderedMap[string] `json:"labels,omitempty,omitzero" yaml:"labels,omitempty"`
	Annotations OrderedMap[string] `json:"annotations,omitempty,omitzero" yaml:"annotations,omitempty"`
}

// FeatureSpec defines the validation, targeting, and interoperability of a catalog Feature.
//
// Schema carries the free-form pillars (values / infra / provides) as JSON
// Schema 2020-12 content with the same three-mode authoring surface as
// ComponentSpec.Schema (inline content; {$import: <path>} for single-file
// replacement; {$ref: oci://...#/schemas/<name>} for SchemaLibrary
// references). UsesSchema and SecretsSchema cover the fixed-shape pillars;
// bridge synthesis composes the three into the unified validation surface.
type FeatureSpec struct {
	Extends              string                      `json:"extends,omitempty" yaml:"extends,omitempty"`
	SecretNames          []string                    `json:"secretNames,omitempty" yaml:"secretNames,omitempty"` // Deprecated: use SecretsSchema.
	SecretsSchema        OrderedMap[SecretSlot]      `json:"secretsSchema,omitempty,omitzero" yaml:"secretsSchema,omitempty"`
	AppliesTo            OrderedMap[string]          `json:"appliesTo,omitempty,omitzero" yaml:"appliesTo,omitempty"`
	AppliesToExpressions []LabelSelectorRequirement  `json:"appliesToExpressions,omitempty" yaml:"appliesToExpressions,omitempty"`
	AppliesToAnyOf       []SelectorTerm              `json:"appliesToAnyOf,omitempty" yaml:"appliesToAnyOf,omitempty"`
	Interoperability     *Interoperability           `json:"interoperability,omitempty" yaml:"interoperability,omitempty"`
	UsesSchema           OrderedMap[UsesSchemaEntry] `json:"usesSchema,omitempty,omitzero" yaml:"usesSchema,omitempty"`
	Schema               yaml.Node                   `json:"schema,omitempty" yaml:"schema,omitempty"`
	// Assertions carries build-time cross-field validation as a map of named
	// Go template rules. Evaluation runs at Step 4b (post-resolution, post-
	// analyzer-gate, pre-render); the first rule whose template invokes
	// {{ fail "message" }} halts the build. Per-instance: a Feature with
	// N attachments produces N evaluations. Strict reference semantics
	// via missingkey=error. See internal/compiler/renderer/assertions.go
	// and internal/compiler/renderer/assertions_step4b.go.
	Assertions OrderedMap[string] `json:"assertions,omitempty,omitzero" yaml:"assertions,omitempty"`
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
	MatchLabels      OrderedMap[string]         `json:"matchLabels,omitempty,omitzero" yaml:"matchLabels,omitempty"`
	MatchExpressions []LabelSelectorRequirement `json:"matchExpressions,omitempty" yaml:"matchExpressions,omitempty"`
}

// Interoperability defines operational safety and interoperability controls for a Feature.
type Interoperability struct {
	ConflictsWith    []string `json:"conflictsWith,omitempty" yaml:"conflictsWith,omitempty"`
	ComponentRefPath string   `json:"componentRefPath,omitempty" yaml:"componentRefPath,omitempty"`
}
