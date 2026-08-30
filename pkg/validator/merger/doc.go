// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package merger composes a chain of JSON Schema 2020-12 schemas into
// a single merged schema with source-position provenance metadata and
// structural narrowing-violation detection.
//
// # Inputs
//
// Merge takes a chain of schemas in author order (root-ancestor first,
// child last). Each schema is a *Schema wrapper carrying the library
// *jsonschema.Schema plus a Source string naming the file:line of
// origin. The caller resolves the chain; Merge is the pure merge primitive.
//
// # Merge semantics
//
// Pairwise left-fold: the root-ancestor merges with the next schema,
// the result merges with the next, etc. Per-keyword merge rules:
//
//   - Scalar constraints (type, min, max, minLength, etc.): child
//     overrides parent if set.
//   - enum, const: child overrides parent if set (narrowing-detection
//     verifies subset separately).
//   - required: union; parent's required-keys plus child's; dedup;
//     sort alphabetically.
//   - properties (map): child keys layered into parent's; for matched
//     keys, recurse pairwise. PropertyOrder on the output is the
//     sorted-alphabetic union.
//   - $defs (map): child entries layered into parent's; for matched
//     keys, child wins.
//   - allOf, anyOf, oneOf (lists): child entries append to parent's
//     list (preserves both authors' constraints).
//   - not: child overrides parent if set.
//   - if/then/else: child overrides parent group atomically.
//   - items, additionalProperties, propertyNames, contains: recurse
//     pairwise if both present; otherwise child if set, else parent.
//   - Extra (x-aggrega-* + other extensions): child entries override
//     parent on key collision; non-colliding entries union.
//     __source_origin is recomputed by the provenance pass.
//
// # Provenance
//
// Post-merge, every node in the merged tree carries an Extra entry:
//
//	Extra["__source_origin"] = map[string]string{
//	    "type": "child.yaml:42",
//	    "min":  "parent.yaml:12",
//	}
//
// One mapping per constraint that contributed to the node. Mixed-origin
// nodes (parent's min plus child's max, both kept) carry multi-entry
// maps. The downstream validator's Finding.SourceOrigin is populated
// from provenance[finding.Keyword] when emitting a Finding for a
// specific keyword violation.
//
// The "__" (double-underscore) prefix marks reserved-namespace for
// merger metadata. Authors writing such keys in their input schemas
// overwrite at their own risk; the provenance pass replaces
// __source_origin unconditionally.
//
// # Narrowing detection
//
// During pairwise merge, when both parent and child supply the same
// constraint keyword, a narrowing check fires:
//
//   - min/minLength/minItems/minProperties: child must be >= parent.
//   - max/maxLength/maxItems/maxProperties: child must be <= parent.
//   - enum: child must be a subset of parent.
//   - const: child must equal parent.
//   - pattern: flag-and-defer heuristic (regex subset checking is
//     deferred to the downstream policy plugin).
//   - type (or types): child's type set must be a subset of parent's.
//
// Each violation appends to the merged result's NarrowingViolations
// slice. The merger does NOT halt on narrowing — the slice is the
// signal that the downstream policy plugin consumes for enforcement.
//
// # Where the chain comes from
//
// This package walks no chain. It is handed one, already assembled and already
// gated, and composes it. The walk lives in pkg/validator, which follows the
// parent each entity declares through its `extends` field, refuses a chain that
// returns to an entity already in it, refuses one that runs past the depth
// bound, and reports both under its own conditions — so a cycle or an over-deep
// chain is refused before any schema reaches the combining step here.
//
// Keeping the walk out of this package is what leaves one extends walk in the
// compiler rather than two that agree until one is edited.
//
// # Determinism
//
// Merge output is byte-identical across two runs of the same input.
// Where the merger constructs new collections from map-typed inputs,
// keys are sorted alphabetically post-construction. PropertyOrder on
// the merged output is the sorted union of input property names so the
// library's MarshalJSON renders properties in a deterministic alphabetic
// order.
package merger
