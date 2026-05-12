// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrValuesMappingShape fires when a Recipe / FeatureRecipe valuesMapping
// node carries a shape outside the three documented modes: not a string
// scalar; not a mapping with a single $import key; or a mapping carrying
// extra siblings beyond $import.
var ErrValuesMappingShape = errors.New("valuesmapping: shape must be a string scalar or {$import: <path-or-oci-url>}")

// ValuesMapping carries one of three content-shape-dispatched forms per
// the schema-language migration's mapping authoring surface:
//
//   - Mode 1 (inline): a Go template string scalar. Inline carries the
//     authored content verbatim; Import stays empty.
//   - Mode 2 (file import): a YAML map of exactly one key
//     `$import: <local-path>`. Import carries the path; Inline stays
//     empty. HasOCIPrefix returns false.
//   - Mode 3 (library import): a YAML map of exactly one key
//     `$import: oci://<host>/<library>@<version>#/mappings/<entry>`.
//     Import carries the OCI URL; HasOCIPrefix returns true.
//
// The struct uses two string fields with a runtime discriminator rather
// than the yaml.Node pattern used by ComponentSpec.Schema because the
// field's shape is bounded — three discrete modes — while Schema
// content is arbitrary JSON Schema 2020-12. Bounded shape benefits
// from type-level capture; unbounded shape benefits from yaml.Node
// pass-through. Future readers comparing the two: the asymmetry is
// principled, not an inconsistency to harmonize.
type ValuesMapping struct {
	// Inline carries the authored Go template content when the YAML
	// value was a string scalar (mode 1). Empty for modes 2 and 3.
	Inline string `json:"-" yaml:"-"`

	// Import carries the authored $import: target when the YAML value
	// was {$import: ...} (modes 2 and 3). Local path (mode 2) or
	// oci:// URL (mode 3); use HasOCIPrefix to discriminate. Named
	// Import (not URL or Ref) to mirror the YAML key authors write;
	// SchemaEntry.ID and MappingEntry.URL are canonical-resolved
	// references on library entries, which is a structurally
	// different concern.
	Import string `json:"-" yaml:"-"`
}

// HasOCIPrefix reports whether v.Import carries an oci:// URL
// (library import mode 3) vs a local path (file import mode 2).
// Helper for consumption-site dispatch.
func (v ValuesMapping) HasOCIPrefix() bool {
	return strings.HasPrefix(v.Import, "oci://")
}

// IsEmpty reports whether neither mode is populated. The validator
// uses this to enforce the "at-least-one-input-kind" gate on
// non-eval Recipes.
func (v ValuesMapping) IsEmpty() bool {
	return v.Inline == "" && v.Import == ""
}

// UnmarshalYAML dispatches by content shape per the schema-language
// migration's mapping authoring surface. A nil node is a no-op so
// callers using *ValuesMapping with omitempty don't trip the sentinel.
func (v *ValuesMapping) UnmarshalYAML(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.ScalarNode:
		v.Inline = node.Value
		return nil
	case yaml.MappingNode:
		// Exactly one key, "$import", with a string-scalar value.
		if len(node.Content) != 2 {
			return fmt.Errorf("%w: got %d keys at line %d", ErrValuesMappingShape, len(node.Content)/2, node.Line)
		}
		keyNode := node.Content[0]
		valNode := node.Content[1]
		if keyNode.Kind != yaml.ScalarNode || keyNode.Value != "$import" {
			return fmt.Errorf("%w: got key %q at line %d", ErrValuesMappingShape, keyNode.Value, keyNode.Line)
		}
		if valNode.Kind != yaml.ScalarNode {
			return fmt.Errorf("%w: $import value must be a string scalar at line %d", ErrValuesMappingShape, valNode.Line)
		}
		v.Import = valNode.Value
		return nil
	default:
		return fmt.Errorf("%w: got kind %d at line %d", ErrValuesMappingShape, node.Kind, node.Line)
	}
}

// MarshalYAML emits the populated mode. Inline → string scalar (literal
// style when the content carries newlines, plain otherwise). Import →
// {$import: <value>} mapping. Empty struct → nil (omitempty on the
// owning field elides the key entirely).
func (v ValuesMapping) MarshalYAML() (any, error) {
	switch {
	case v.Inline != "":
		node := &yaml.Node{
			Kind:  yaml.ScalarNode,
			Value: v.Inline,
		}
		if strings.ContainsRune(v.Inline, '\n') {
			node.Style = yaml.LiteralStyle
		}
		return node, nil
	case v.Import != "":
		return map[string]string{"$import": v.Import}, nil
	default:
		return nil, nil
	}
}
