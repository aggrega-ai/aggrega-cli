// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package merger

import (
	"sort"

	"github.com/google/jsonschema-go/jsonschema"
)

// Schema wraps a library *jsonschema.Schema with merger-specific
// metadata. Source names the file:line of the schema's origin (or any
// string the caller wants attributed to the schema's contributions in
// merged-output provenance). NarrowingViolations is the side-band
// signal populated by Merge: each violation reports a child constraint
// that widens its parent — the merger does not halt on narrowing, so
// callers (typically the publishing-discipline policy plugin) consume
// the slice for enforcement.
//
// Source is the value that surfaces in __source_origin Extra entries
// of the merged tree; conventional values follow the file:line shape
// ("path/to/component.yaml:42"), but the merger treats Source as an
// opaque string and reproduces it verbatim.
//
// The wrapper carries the library Schema as an embedded pointer, so
// every Schema-typed field on jsonschema.Schema is reachable directly
// (e.g., s.Properties, s.Defs). Callers who want the inner Schema for
// library-side operations dereference s.Schema.
type Schema struct {
	*jsonschema.Schema

	// Source names the file:line of the schema's origin. Merger
	// reproduces this string verbatim in the merged tree's
	// __source_origin Extra entries.
	Source string

	// NarrowingViolations is populated by Merge: each entry names a
	// child constraint that widens its parent constraint. Empty on
	// schemas that have not been merged (or whose merge produced no
	// violations). Signal-only — the merger does not halt.
	NarrowingViolations []NarrowingViolation
}

// NarrowingViolation reports a child constraint that widens its
// parent constraint during merge. Signal-only: appended to the merged
// result's NarrowingViolations slice; the merger does not halt.
//
// FieldPath is instance-relative (matching the validator's Finding
// convention): "port" rather than "properties.port". Keyword names
// the constraint that widened ("min", "enum", "pattern", etc.).
// ParentSource and ChildSource carry the file:line origins of the
// parent and child constraints respectively — sourced from the
// merger's per-constraint provenance map so multi-level chains
// (grandparent's min surviving through parent) report the actual
// originating schema's Source rather than a synthetic intermediate
// merger state.
type NarrowingViolation struct {
	FieldPath    string
	Keyword      string
	Detail       string
	ParentSource string
	ChildSource  string
}

// Merge composes a chain of schemas (root-ancestor first, child last)
// into a single merged Schema with provenance metadata and any
// narrowing violations detected during pairwise composition. The
// caller supplies the chain in author order; Merge does not walk
// extends links, which the single walk in pkg/validator does.
//
// Pairwise left-fold: schemas[0] merges with schemas[1], the result
// merges with schemas[2], etc. Each pairwise step records per-
// constraint provenance into a path-keyed map that the post-merge
// walk converts into Extra["__source_origin"] entries.
//
// Returns ErrMergeInvalid wrapped in *MergeError when the input chain
// is empty. A single-element chain returns the element as-is with
// provenance tags reflecting the element's Source.
//
// Narrowing violations populate the returned Schema's
// NarrowingViolations slice; an error is NOT returned on narrowing
// (signal-only by contract). Returns a non-nil error only
// on structural failures (empty input, nil schemas in the chain).
func Merge(schemas []*Schema) (*Schema, error) {
	if len(schemas) == 0 {
		return nil, &MergeError{
			Detail: "merge input chain is empty",
			Err:    ErrMergeInvalid(),
		}
	}
	for i, s := range schemas {
		if s == nil || s.Schema == nil {
			return nil, &MergeError{
				Detail: nilSchemaDetail(i),
				Err:    ErrMergeInvalid(),
			}
		}
	}

	// Provenance map: provenance[path][keyword] = source. Populated
	// during the pairwise merges; applied to the merged tree's Extra
	// entries by the post-merge provenance pass.
	provenance := map[string]map[string]string{}

	// Initialise the fold with a clone of schemas[0]. Pairwise merges
	// produce new *Schema values that own their merged trees; the
	// input chain is not mutated.
	acc := cloneSchema(schemas[0])
	seedProvenance(acc, "", schemas[0].Source, provenance)

	var violations []NarrowingViolation

	for i := 1; i < len(schemas); i++ {
		next := schemas[i]
		merged, stepViolations := mergePair(acc, next, "", provenance)
		acc = merged
		violations = append(violations, stepViolations...)
	}

	applyProvenance(acc.Schema, "", provenance)

	// Order the per-entity narrowing notices deterministically at the source.
	// A single merge step's multi-field narrowings are produced in map-iteration
	// order; the closure walk sorts ACROSS entities but not within one, so an
	// entity that widens several inherited constraints would otherwise emit its
	// notices in a run-varying order. FieldPath → Keyword → Detail already makes
	// the rendered advisory deterministic; ParentSource → ChildSource extend the
	// key to a total order over the full struct so the slice itself is canonical.
	sort.Slice(violations, func(i, j int) bool {
		a, b := violations[i], violations[j]
		switch {
		case a.FieldPath != b.FieldPath:
			return a.FieldPath < b.FieldPath
		case a.Keyword != b.Keyword:
			return a.Keyword < b.Keyword
		case a.Detail != b.Detail:
			return a.Detail < b.Detail
		case a.ParentSource != b.ParentSource:
			return a.ParentSource < b.ParentSource
		default:
			return a.ChildSource < b.ChildSource
		}
	})
	acc.NarrowingViolations = violations
	return acc, nil
}

// nilSchemaDetail returns a human-readable detail for a nil schema in
// the input chain.
func nilSchemaDetail(index int) string {
	return "merge input contains a nil schema at index " + itoa(index)
}

// itoa is a small int-to-string helper that avoids the strconv import
// at the package's top level. The merger imports strconv elsewhere
// (in merge.go for numeric comparisons); centralising the conversion
// here keeps the error formatter self-contained.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		digits[i] = '-'
	}
	return string(digits[i:])
}
