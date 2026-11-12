// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package completion

import "github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"

// ContextKind names the completion-only cursor positions that
// yamlpos.Classify does NOT recognize. Navigation cursors (Extends,
// ComponentsType, FeaturesType, RecipeImplements, ManifestRecipeReference,
// ReleaseAggregatePath, ReleaseManifestPath, UsesTarget, MetadataName,
// SchemaProperty) are handled separately via their yamlpos
// ContextKind in the dispatcher; this enum covers the residual
// completion-specific contexts.
type ContextKind int

const (
	// ContextKindUnknown — cursor does not match any recognized
	// completion context. Dispatcher returns an empty candidate list.
	ContextKindUnknown ContextKind = iota

	// ContextKindKind — cursor on the "kind:" value at the top of an
	// entity document. Candidates: the entity-kinds closed set.
	ContextKindKind

	// ContextKindAPIVersion — cursor on the "apiVersion:" value at
	// the top of an entity document. Candidates: the API-versions
	// closed set.
	ContextKindAPIVersion

	// ContextKindLabelValue — cursor on the value of
	// "metadata.labels.<label-name>:". Candidates depend on the
	// label-name (e.g., component.aggrega.ai/category → categories).
	ContextKindLabelValue

	// ContextKindSchemaKeyword — cursor on the KEY side inside the
	// spec.schema subtree (or on a value position that doesn't carry
	// a more-specific candidate set). Candidates: vanilla JSON Schema
	// keywords + Aggrega extensions.
	ContextKindSchemaKeyword

	// ContextKindSchemaTypeValue — cursor on the VALUE side of a
	// `type:` keyword inside spec.schema (e.g., `properties.foo.type:
	// <here>`, `items.type: <here>`). Candidates: the JSON Schema
	// primitive type names (object, string, integer, etc.).
	ContextKindSchemaTypeValue
)

// ClassifyForCompletion inspects the cursor's path + side and returns
// the matching completion-specific ContextKind. Called when
// yamlpos.Classify returned ContextKindUnknown (the cursor is not a
// navigation reference shape) — this classifier picks up the
// completion-only contexts.
//
// The classifier mirrors yamlpos.ClassifyPath's strip-and-suffix-match
// pattern (strip leading "spec", strip recursive "(subsystems, <key>)"
// pairs greedily) so subsystem-nested cases are handled uniformly.
//
// When no completion-only suffix matches, ContextKindUnknown is
// returned and the dispatcher emits an empty candidate list.
func ClassifyForCompletion(path []string, side yamlpos.ContextSide) ContextKind {
	if len(path) == 0 {
		return ContextKindUnknown
	}

	// Top-of-document scalar fields.
	if len(path) == 1 {
		switch path[0] {
		case "kind":
			return ContextKindKind
		case "apiVersion":
			return ContextKindAPIVersion
		}
	}

	// metadata.labels.<label-name>:  — three elements, with optional
	// LabelValue dispatch on the cursor side (Value-side cursors land
	// inside the label-value scalar; Key-side cursors would be on the
	// label-name itself, which is a separate completion shape —
	// label-name completion is deferred).
	if len(path) == 3 && path[0] == "metadata" && path[1] == "labels" && side == yamlpos.ContextSideValue {
		return ContextKindLabelValue
	}

	if path[0] != "spec" {
		return ContextKindUnknown
	}

	rest := path[1:]

	// Strip recursive (subsystems, <key>) pairs for completion
	// contexts that can nest (currently no spec.<x>.provider equivalent
	// nests; this is forward-compat with hover's strip-and-match
	// pattern).
	for len(rest) >= 2 && rest[0] == "subsystems" {
		rest = rest[2:]
	}

	// spec.schema.* — sub-branches by cursor side + trailing path
	// pattern:
	//
	//   - VALUE side AND the last path segment is "type":
	//     scalar-form type cursor (e.g., `type: <here>`). Offer JSON
	//     Schema primitive types. Parent-keyword check disambiguates:
	//     when "type" is itself a user-named property under a
	//     JSON Schema container keyword (properties, $defs, etc.),
	//     primitives are semantically wrong and the classifier
	//     returns Unknown.
	//
	//   - VALUE side AND the path ends with ["type", "<numeric>"]:
	//     array-form type cursor (e.g., `type: [string, <here>]`).
	//     Same primitives, same parent-keyword check.
	//
	//   - KEY side anywhere inside schema: keyword candidates
	//     (vanilla JSON Schema 2020-12 + Aggrega x-aggrega-* extensions).
	//
	//   - VALUE side at any other key: deferred — no candidates yet
	//     (free-form scalars like description, pattern, format do not
	//     have a closed enumeration today).
	if len(rest) >= 2 && rest[0] == "schema" {
		if side == yamlpos.ContextSideValue {
			// Scalar form: last segment is "type".
			if rest[len(rest)-1] == "type" {
				if !typeKeyIsUserNamedProperty(rest, len(rest)-1) {
					return ContextKindSchemaTypeValue
				}
				return ContextKindUnknown
			}
			// Array form: path ends with ["type", "<numeric>"].
			if len(rest) >= 2 && isNumericSegment(rest[len(rest)-1]) && rest[len(rest)-2] == "type" {
				if !typeKeyIsUserNamedProperty(rest, len(rest)-2) {
					return ContextKindSchemaTypeValue
				}
				return ContextKindUnknown
			}
		}
		if side == yamlpos.ContextSideKey {
			return ContextKindSchemaKeyword
		}
		return ContextKindUnknown
	}

	return ContextKindUnknown
}

// typeKeyIsUserNamedProperty reports whether the "type" segment at
// rest[typeIdx] is a USER-NAMED property name rather than the JSON
// Schema keyword. The check looks at the parent segment via
// isActingAsContainer — when the parent is acting as a JSON Schema
// container keyword (properties, $defs, definitions,
// patternProperties), then "type" is a property NAME under that
// container, and primitive-type candidates are semantically wrong
// for the cursor.
//
// Disambiguates structurally-identical paths via the recursive
// container check:
//   - [schema, properties, port, type] — port is user-named (not a
//     container); type is the JSON Schema keyword; primitives correct.
//   - [schema, properties, type] — outer properties is the container;
//     type is user-named under it; primitives wrong (Unknown).
//   - [schema, properties, properties, type] — inner properties is
//     itself a user-named property (its parent properties acts as
//     container); type is the JSON Schema keyword of the user-named
//     property; primitives correct.
//   - [schema, properties, properties, properties, type] — chains
//     further; the recursive check handles arbitrary nesting depth.
//
// Returns false (treat as JSON Schema keyword) when the preceding
// segment cannot be inspected (typeIdx == 0).
func typeKeyIsUserNamedProperty(rest []string, typeIdx int) bool {
	if typeIdx <= 0 {
		return false
	}
	return isActingAsContainer(rest, typeIdx-1)
}

// isActingAsContainer reports whether the segment at idx is acting
// as a JSON Schema container keyword (rather than being a user-named
// property whose name collides with a reserved keyword).
//
// A segment is acting as a container iff:
//   - Its name is one of the JSON Schema container keywords
//     (properties, $defs, definitions, patternProperties).
//   - AND its parent is NOT itself acting as a container.
//
// The parent check chains recursively. A user-named property whose
// value is a sub-schema introduces a fresh JSON-Schema-keyword
// alternation — the user-named property's children are JSON Schema
// keywords (which may again include reserved-name containers, which
// in turn may host user-named properties again).
//
// Without the recursive parent check, a user who names a property
// literally "properties" would have its inner properties (the
// user-named property's OWN container) treated as a user-named
// property too, producing wrong classifications at arbitrary
// nesting depth.
func isActingAsContainer(rest []string, idx int) bool {
	if idx < 0 || idx >= len(rest) {
		return false
	}
	if !isJSONSchemaContainerKeyword(rest[idx]) {
		return false
	}
	if idx == 0 {
		// No parent left in the path to inspect — segment has the
		// reserved name and is at the top of rest; treat as container.
		return true
	}
	return !isActingAsContainer(rest, idx-1)
}

// isJSONSchemaContainerKeyword reports whether s is one of the
// JSON Schema keywords whose children are user-named properties /
// definitions (rather than further JSON Schema keywords).
func isJSONSchemaContainerKeyword(s string) bool {
	switch s {
	case "properties", "$defs", "definitions", "patternProperties":
		return true
	}
	return false
}

// isNumericSegment reports whether s is a non-empty decimal-integer
// string. Used to identify sequence-element path segments (yamlpos
// represents sequence indices as decimal strings).
func isNumericSegment(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
