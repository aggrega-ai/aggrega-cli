// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"bytes"
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

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
// replacement; {$ref: <Library>#/schemas/<name>} for SchemaLibrary
// references by bare-name identity — version from the per-Release lockfile,
// no registry host). UsesSchema and SecretsSchema cover the fixed-shape pillars;
// bridge synthesis composes the three into the unified validation surface.
type FeatureSpec struct {
	Extends          string                      `json:"extends,omitempty" yaml:"extends,omitempty"`
	SecretNames      []string                    `json:"secretNames,omitempty" yaml:"secretNames,omitempty"` // Deprecated: use SecretsSchema.
	SecretsSchema    OrderedMap[SecretSlot]      `json:"secretsSchema,omitempty,omitzero" yaml:"secretsSchema,omitempty"`
	AppliesTo        string                      `json:"appliesTo,omitempty" yaml:"appliesTo,omitempty"`
	AppliesToAnyOf   []SelectorTerm              `json:"appliesToAnyOf,omitempty" yaml:"appliesToAnyOf,omitempty"`
	Interoperability *Interoperability           `json:"interoperability,omitempty" yaml:"interoperability,omitempty"`
	UsesSchema       OrderedMap[UsesSchemaEntry] `json:"usesSchema,omitempty,omitzero" yaml:"usesSchema,omitempty"`
	Schema           yaml.Node                   `json:"schema,omitempty" yaml:"schema,omitempty"`
	// Assertions carries build-time cross-field validation as a map of named
	// Go template rules. Evaluation runs at Step 4b (post-resolution, post-
	// analyzer-gate, pre-render); the first rule whose template invokes
	// {{ fail "message" }} halts the build. Per-instance: a Feature with
	// N attachments produces N evaluations. Strict reference semantics
	// via missingkey=error. See internal/compiler/renderer/assertions.go
	// and internal/compiler/renderer/assertions_step4b.go.
	Assertions OrderedMap[string] `json:"assertions,omitempty,omitzero" yaml:"assertions,omitempty"`
}

// SelectorTerm is one item in appliesToAnyOf (OR logic between items). An
// item carries exactly one of two arms, following the authored shape: a
// string-form label selector (the same grammar appliesTo accepts;
// requirements inside the string AND together) or a flat key-value map
// matched by equality only. Marshalling re-emits the authored arm — a
// scalar stays a scalar, a map stays a map with author-ordered keys — so
// re-emission of parsed catalog content is deterministic.
// Schema: feature-v1alpha1.schema.json#/properties/spec/properties/appliesToAnyOf/items
type SelectorTerm struct {
	// Selector is the string-form arm. Meaningful only when IsMap
	// reports false.
	Selector string
	// Labels is the flat-map arm (equality-only matching). Meaningful
	// only when IsMap reports true.
	Labels OrderedMap[string]

	isMap bool
}

// IsMap reports which arm the item carries: true for the flat key-value
// map form, false for the string-form selector.
func (t SelectorTerm) IsMap() bool { return t.isMap }

// StringTerm constructs the string-form arm of a SelectorTerm.
func StringTerm(selector string) SelectorTerm {
	return SelectorTerm{Selector: selector}
}

// MapTerm constructs the flat-map arm of a SelectorTerm.
func MapTerm(labels OrderedMap[string]) SelectorTerm {
	return SelectorTerm{Labels: labels, isMap: true}
}

// UnmarshalYAML dispatches on the authored node shape: a scalar populates
// the string-form arm, a mapping populates the flat-map arm. Any other
// shape is rejected naming the two accepted forms.
func (t *SelectorTerm) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		var s string
		if err := node.Decode(&s); err != nil {
			return err
		}
		*t = StringTerm(s)
		return nil
	case yaml.MappingNode:
		var m OrderedMap[string]
		if err := node.Decode(&m); err != nil {
			return err
		}
		*t = MapTerm(m)
		return nil
	default:
		return fmt.Errorf("appliesToAnyOf item must be a selector string or a flat key-value map (line %d)", node.Line)
	}
}

// MarshalYAML re-emits the authored arm.
func (t SelectorTerm) MarshalYAML() (any, error) {
	if t.isMap {
		return t.Labels, nil
	}
	return t.Selector, nil
}

// UnmarshalJSON dispatches on the JSON shape: a string populates the
// string-form arm, an object populates the flat-map arm.
func (t *SelectorTerm) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return fmt.Errorf("appliesToAnyOf item must be a selector string or a flat key-value map")
	}
	switch trimmed[0] {
	case '"':
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return err
		}
		*t = StringTerm(s)
		return nil
	case '{':
		var m OrderedMap[string]
		if err := json.Unmarshal(trimmed, &m); err != nil {
			return err
		}
		*t = MapTerm(m)
		return nil
	default:
		return fmt.Errorf("appliesToAnyOf item must be a selector string or a flat key-value map")
	}
}

// MarshalJSON re-emits the authored arm.
func (t SelectorTerm) MarshalJSON() ([]byte, error) {
	if t.isMap {
		return json.Marshal(t.Labels)
	}
	return json.Marshal(t.Selector)
}

// Interoperability defines operational safety and interoperability controls for a Feature.
type Interoperability struct {
	ConflictsWith    []string `json:"conflictsWith,omitempty" yaml:"conflictsWith,omitempty"`
	ComponentRefPath string   `json:"componentRefPath,omitempty" yaml:"componentRefPath,omitempty"`
}
