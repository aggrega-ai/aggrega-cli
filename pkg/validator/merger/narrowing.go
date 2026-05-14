// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package merger

import (
	"fmt"
	"reflect"
)

// detectNarrowing inspects the parent + child schemas for overlapping
// per-keyword constraints. When both sides supply the same keyword,
// the child must be at-least-as-narrow as the parent; otherwise a
// NarrowingViolation lands on the result slice.
//
// ParentSource is sourced from the provenance map's entry for
// (pathPrefix, keyword) — the actual originator of the parent
// constraint, which may sit several levels up the chain rather than
// at the immediately-preceding pairwise input. ChildSource is the
// child's own Source string.
//
// Signal-only: detectNarrowing never returns an error; the caller
// appends violations to the merged result's NarrowingViolations slice.
func detectNarrowing(parent, child *Schema, pathPrefix string, provenance map[string]map[string]string) []NarrowingViolation {
	var violations []NarrowingViolation

	p := parent.Schema
	c := child.Schema

	resolveParentSource := func(keyword string) string {
		if entries, ok := provenance[pathPrefix]; ok {
			if source, ok := entries[keyword]; ok {
				return source
			}
		}
		return parent.Source
	}
	childSource := child.Source

	// Lower-bound keywords — child must be >= parent (child narrowing
	// raises the floor; child widening lowers it, which violates).
	type intBound struct {
		keyword string
		parent  *int
		child   *int
	}
	intLowerBounds := []intBound{
		{"minLength", p.MinLength, c.MinLength},
		{"minItems", p.MinItems, c.MinItems},
		{"minProperties", p.MinProperties, c.MinProperties},
		{"minContains", p.MinContains, c.MinContains},
	}
	for _, b := range intLowerBounds {
		if b.parent != nil && b.child != nil && *b.child < *b.parent {
			violations = append(violations, NarrowingViolation{
				FieldPath:    pathPrefix,
				Keyword:      b.keyword,
				Detail:       fmt.Sprintf("child %s=%d widens parent %s=%d", b.keyword, *b.child, b.keyword, *b.parent),
				ParentSource: resolveParentSource(b.keyword),
				ChildSource:  childSource,
			})
		}
	}

	// Upper-bound keywords — child must be <= parent (child narrowing
	// lowers the ceiling; child widening raises it, which violates).
	intUpperBounds := []intBound{
		{"maxLength", p.MaxLength, c.MaxLength},
		{"maxItems", p.MaxItems, c.MaxItems},
		{"maxProperties", p.MaxProperties, c.MaxProperties},
		{"maxContains", p.MaxContains, c.MaxContains},
	}
	for _, b := range intUpperBounds {
		if b.parent != nil && b.child != nil && *b.child > *b.parent {
			violations = append(violations, NarrowingViolation{
				FieldPath:    pathPrefix,
				Keyword:      b.keyword,
				Detail:       fmt.Sprintf("child %s=%d widens parent %s=%d", b.keyword, *b.child, b.keyword, *b.parent),
				ParentSource: resolveParentSource(b.keyword),
				ChildSource:  childSource,
			})
		}
	}

	// Numeric lower-bound (minimum, exclusiveMinimum).
	if p.Minimum != nil && c.Minimum != nil && *c.Minimum < *p.Minimum {
		violations = append(violations, NarrowingViolation{
			FieldPath:    pathPrefix,
			Keyword:      "minimum",
			Detail:       fmt.Sprintf("child minimum=%v widens parent minimum=%v", *c.Minimum, *p.Minimum),
			ParentSource: resolveParentSource("minimum"),
			ChildSource:  childSource,
		})
	}
	if p.ExclusiveMinimum != nil && c.ExclusiveMinimum != nil && *c.ExclusiveMinimum < *p.ExclusiveMinimum {
		violations = append(violations, NarrowingViolation{
			FieldPath:    pathPrefix,
			Keyword:      "exclusiveMinimum",
			Detail:       fmt.Sprintf("child exclusiveMinimum=%v widens parent exclusiveMinimum=%v", *c.ExclusiveMinimum, *p.ExclusiveMinimum),
			ParentSource: resolveParentSource("exclusiveMinimum"),
			ChildSource:  childSource,
		})
	}

	// Numeric upper-bound (maximum, exclusiveMaximum).
	if p.Maximum != nil && c.Maximum != nil && *c.Maximum > *p.Maximum {
		violations = append(violations, NarrowingViolation{
			FieldPath:    pathPrefix,
			Keyword:      "maximum",
			Detail:       fmt.Sprintf("child maximum=%v widens parent maximum=%v", *c.Maximum, *p.Maximum),
			ParentSource: resolveParentSource("maximum"),
			ChildSource:  childSource,
		})
	}
	if p.ExclusiveMaximum != nil && c.ExclusiveMaximum != nil && *c.ExclusiveMaximum > *p.ExclusiveMaximum {
		violations = append(violations, NarrowingViolation{
			FieldPath:    pathPrefix,
			Keyword:      "exclusiveMaximum",
			Detail:       fmt.Sprintf("child exclusiveMaximum=%v widens parent exclusiveMaximum=%v", *c.ExclusiveMaximum, *p.ExclusiveMaximum),
			ParentSource: resolveParentSource("exclusiveMaximum"),
			ChildSource:  childSource,
		})
	}

	// enum — child must be a subset of parent.
	if len(p.Enum) > 0 && len(c.Enum) > 0 {
		if !enumIsSubset(c.Enum, p.Enum) {
			violations = append(violations, NarrowingViolation{
				FieldPath:    pathPrefix,
				Keyword:      "enum",
				Detail:       "child enum widens parent enum (child contains values not present in parent)",
				ParentSource: resolveParentSource("enum"),
				ChildSource:  childSource,
			})
		}
	}

	// const — child must equal parent (any change is widening).
	if p.Const != nil && c.Const != nil && !reflect.DeepEqual(*p.Const, *c.Const) {
		violations = append(violations, NarrowingViolation{
			FieldPath:    pathPrefix,
			Keyword:      "const",
			Detail:       fmt.Sprintf("child const=%v differs from parent const=%v", *c.Const, *p.Const),
			ParentSource: resolveParentSource("const"),
			ChildSource:  childSource,
		})
	}

	// pattern — flag-and-defer heuristic.
	if p.Pattern != "" && c.Pattern != "" && p.Pattern != c.Pattern {
		violations = append(violations, NarrowingViolation{
			FieldPath:    pathPrefix,
			Keyword:      "pattern",
			Detail:       fmt.Sprintf("child pattern=%q differs from parent pattern=%q; exact regex subset check deferred to policy plugin", c.Pattern, p.Pattern),
			ParentSource: resolveParentSource("pattern"),
			ChildSource:  childSource,
		})
	}

	// type — child's type must be the same as parent's or a subtype.
	// JSON Schema 2020-12 has no formal type hierarchy beyond
	// equality + (single-type <-> multi-types-list compatibility), so
	// the check is conservative: flag mismatches outright. A future
	// revision can layer a stricter hierarchy if needed.
	if p.Type != "" && c.Type != "" && p.Type != c.Type {
		violations = append(violations, NarrowingViolation{
			FieldPath:    pathPrefix,
			Keyword:      "type",
			Detail:       fmt.Sprintf("child type=%q differs from parent type=%q", c.Type, p.Type),
			ParentSource: resolveParentSource("type"),
			ChildSource:  childSource,
		})
	}
	// types (multi-type) — child's set must be a subset of parent's.
	if len(p.Types) > 0 && len(c.Types) > 0 {
		if !stringSliceIsSubset(c.Types, p.Types) {
			violations = append(violations, NarrowingViolation{
				FieldPath:    pathPrefix,
				Keyword:      "types",
				Detail:       "child types list contains values not present in parent",
				ParentSource: resolveParentSource("types"),
				ChildSource:  childSource,
			})
		}
	}

	return violations
}

// enumIsSubset returns true when every element of child appears in
// parent. Uses reflect.DeepEqual for element comparison — enum
// entries can be any JSON-typed value.
func enumIsSubset(child, parent []any) bool {
	for _, c := range child {
		found := false
		for _, p := range parent {
			if reflect.DeepEqual(c, p) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// stringSliceIsSubset returns true when every element of child appears
// in parent.
func stringSliceIsSubset(child, parent []string) bool {
	for _, c := range child {
		found := false
		for _, p := range parent {
			if c == p {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
