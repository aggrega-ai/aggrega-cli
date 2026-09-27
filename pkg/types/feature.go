// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"bytes"
	"encoding/json"
	"fmt"

	"go.yaml.in/yaml/v3"
)

//go:generate go run ../../internal/tools/typegen feature_gen.go

// Feature's struct declarations (Feature, FeatureMetadata, FeatureSpec)
// are generated into feature_gen.go from the published feature-v1alpha1
// schema; their godoc is the schema's descriptions. This hand-written
// companion holds the polymorphic SelectorTerm (with its custom
// marshalers — the generator emits a []SelectorTerm field referencing it
// but never the type itself) and the Interoperability nested struct the
// generated spec references by pointer. The shared Maintainer type the
// generated metadata references lives in common.go; the shared AttachmentSlot
// and UsesSchemaEntry value types live in component.go.

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
	// ConflictsWith names the Features this Feature cannot reach a component
	// alongside. A build halts when one component's merged attachment set holds
	// this Feature and one named here, compared by name with any version removed.
	ConflictsWith []string `json:"conflictsWith,omitempty" yaml:"conflictsWith,omitempty"`
}
