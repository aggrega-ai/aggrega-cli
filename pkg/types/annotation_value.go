// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package types

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

// AnnotationValue is the parsed value carried in annotation fields on
// Aggregate and Release authoring surfaces. Three shapes:
//
//   - Scalar: a single string value. The binding's Maps must have
//     exactly one entry; the scalar routes to that entry's subkey +
//     pillar.
//   - Structured: a map of subkey → subvalue. Each subkey routes per
//     the binding's Maps to the corresponding Feature schema field;
//     subvalues are validated against the Feature's declared types
//     at expansion time.
//   - Nil: explicit YAML null at the value site. Carried as a nil
//     pointer in the parent OrderedMap; opts out of the inherited
//     binding for the cascade scope.
//
// The type is conceptually a sum type but implemented as a record
// with discriminated fields. Exactly one of Scalar / Structured is
// populated when the AnnotationValue is non-nil and non-empty; the
// YAML node Kind drives which field receives the value. The IsScalar
// and IsStructured predicates expose the discrimination at the
// consumption site.
//
// Authoring shapes:
//
//	# Scalar
//	annotations:
//	  feature.myco/rewrite-host: "api.example.com"
//
//	# Structured (one annotation key carrying multiple subkeys)
//	annotations:
//	  feature.myco/db-pool:
//	    pool_size: 20
//	    max_connections: 100
//
//	# Whole-value null (opt out the inherited binding wholesale)
//	annotations:
//	  feature.myco/db-pool: null
//
//	# Per-subkey null (structured value with a subkey opt-out)
//	annotations:
//	  feature.myco/db-pool:
//	    pool_size: 20
//	    max_connections: null
//
// Cascade-merge semantics for structured values live in the resolver's
// annotation walker; the parse-layer responsibility is to faithfully
// represent the three shapes plus the per-subkey nil-subvalue signal
// that the merge step consumes.
type AnnotationValue struct {
	Scalar     *string         `yaml:"-" json:"scalar,omitempty"`
	Structured OrderedMap[any] `yaml:"-" json:"structured,omitempty,omitzero"`
}

// IsScalar reports whether the value carries a scalar string. Returns
// true iff the receiver is non-nil and its Scalar field is non-nil.
func (av *AnnotationValue) IsScalar() bool {
	return av != nil && av.Scalar != nil
}

// IsStructured reports whether the value carries a structured map
// with at least one entry. Returns false for nil receivers and for
// AnnotationValues whose Structured field is empty.
func (av *AnnotationValue) IsStructured() bool {
	return av != nil && av.Structured.Len() > 0
}

// IsEmpty reports whether the value carries no content — neither a
// scalar nor any structured entries. Returns true for nil receivers
// and for non-nil AnnotationValues with both fields empty (the third
// state the defensive UnmarshalYAML null branch can produce).
//
// Used by the cascade-merge logic to detect whole-value opt-out
// signals regardless of whether the integration path produced a nil
// pointer (yaml.v3's primary null behaviour at the parent map level)
// or a non-nil pointer to an empty AnnotationValue (the defensive
// parse path inside UnmarshalYAML). Merging on `IsEmpty()` rather
// than `value == nil` is the belt-and-suspenders predicate the
// merge primitive uses to keep the opt-out semantic intact against
// future integration changes.
func (av *AnnotationValue) IsEmpty() bool {
	return av == nil || (!av.IsScalar() && !av.IsStructured())
}

// ScalarOnly projects an OrderedMap[*AnnotationValue] (the post-flatten
// cascade output for downstream Feature expansion) into an
// OrderedMap[string] suitable for render-context template substitution.
// Entries whose value is a Scalar AnnotationValue contribute their
// string value at the same key; entries whose value is Structured,
// IsEmpty, or otherwise non-scalar are excluded from the projection.
//
// The projection preserves OrderedMap author order through the
// underlying All() iterator. Empty input produces an empty output.
//
// Used by the compiler renderer's template-context construction sites
// — `.context.annotations.*` and `.parent_boundary.annotations` —
// where annotations surface as string-keyed string-valued maps for
// template substitution. Structured annotations are routing
// instructions for Feature expansion (they fire only through
// annotation expansion); they do not surface in template substrate.
func ScalarOnly(in OrderedMap[*AnnotationValue]) OrderedMap[string] {
	out := OrderedMap[string]{}
	for k, v := range in.All() {
		if v == nil || !v.IsScalar() {
			continue
		}
		out.Set(k, *v.Scalar)
	}
	return out
}

// UnmarshalYAML populates the AnnotationValue from a YAML node,
// dispatching on the node Kind. The unmarshaller never receives null-
// tagged values on a pointer field: yaml.v3 stores nil for **T when
// the YAML value is null, so the parent OrderedMap[*AnnotationValue]
// stores a nil pointer for explicit-null entries and the cascade-
// merge step interprets the nil pointer as a whole-value opt-out
// signal.
//
// Scalar nodes (non-null) populate Scalar with the node's string
// value. Empty scalars produce an empty string (Scalar non-nil
// pointing to ""); this preserves the operator's authoring intent
// for a deliberately-empty value distinct from null opt-out.
//
// Mapping nodes populate Structured by decoding through
// OrderedMap[any]'s own unmarshaller. Null subvalues inside the
// mapping are preserved as nil entries in the resulting OrderedMap —
// the cascade-merge step inspects these to fire per-subkey opt-outs.
// Dropping null-tagged children at parse time would leave the merge
// with no signal to act on; preservation is load-bearing.
//
// Sequence nodes are rejected with a clear error naming the three
// accepted authoring shapes (scalar, object, null) so operators
// hitting this from a YAML authoring mistake (e.g., a list literal
// instead of an object) get a meaningful diagnostic.
func (av *AnnotationValue) UnmarshalYAML(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.ScalarNode:
		if isYAMLNull(node) {
			// Defensive: yaml.v3 normally stores nil for **AnnotationValue
			// at the parent map; if a null-tagged value reaches this
			// method, leave both fields empty so IsScalar /
			// IsStructured report false. The caller decides whether to
			// retain the AnnotationValue or replace with a nil pointer.
			return nil
		}
		s := node.Value
		av.Scalar = &s
		return nil
	case yaml.MappingNode:
		if err := node.Decode(&av.Structured); err != nil {
			return fmt.Errorf("annotation value at line %d: structured decode failed: %w", node.Line, err)
		}
		return nil
	case yaml.SequenceNode:
		return fmt.Errorf(
			"ANNOTATION_PARSE_ERROR: annotation value at line %d must be one of: scalar string, object (map of subkeys), or null — got YAML sequence",
			node.Line,
		)
	default:
		return fmt.Errorf(
			"ANNOTATION_PARSE_ERROR: annotation value at line %d carries unsupported YAML node kind %v (expected scalar, object, or null)",
			node.Line, node.Kind,
		)
	}
}

// MarshalYAML emits the AnnotationValue in its parsed shape. Scalar
// values emit as YAML scalars; structured values emit as YAML
// mappings; an empty AnnotationValue emits as null. The marshaller
// is symmetric with UnmarshalYAML so round-trips preserve shape.
func (av AnnotationValue) MarshalYAML() (interface{}, error) {
	if av.Scalar != nil {
		return *av.Scalar, nil
	}
	if av.Structured.Len() > 0 {
		return av.Structured, nil
	}
	return nil, nil
}

// isYAMLNull reports whether a scalar node represents the YAML null
// value. yaml.v3 normalises explicit nulls (`null`, `~`, empty value
// with the !!null tag) but a permissive check covers cases where the
// tag is implicit on an empty scalar.
func isYAMLNull(node *yaml.Node) bool {
	if node == nil {
		return true
	}
	if node.Tag == "!!null" {
		return true
	}
	if node.Value == "null" || node.Value == "~" {
		return true
	}
	if node.Value == "" && node.Style == 0 {
		// Untagged empty scalar with no quoting style: YAML treats
		// this as null. Distinguish from an explicit empty-quoted
		// string (Style != 0) which is a deliberately-empty scalar.
		return true
	}
	return false
}
