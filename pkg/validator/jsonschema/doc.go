// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package jsonschema wraps the google/jsonschema-go library behind an
// adapter interface so the compiler's validator pipeline can run JSON
// Schema 2020-12 validation while honoring Aggrega's `x-aggrega-*`
// extension keywords. The adapter is the swap boundary between the
// compiler core and the concrete JSON Schema implementation; a future
// Wasm-validator socket extraction supplies an alternative implementation
// honoring the same Validator contract.
//
// The adapter recognises four extension keywords and overrides the
// library's default semantics for the first two:
//
//   - x-aggrega-when:  template predicate gating a JSON Schema subtree.
//     When present on a node, the adapter ignores the node's if:/then:/
//     else: keywords entirely; instead, it evaluates the predicate
//     against the instance and the surrounding parameter context, then
//     validates the instance against the then: branch (truthy) or the
//     else: branch (falsy + else: authored) or skips (falsy + no else:).
//   - x-aggrega-validate:  per-field template predicate; falsy result
//     emits a Finding carrying the x-aggrega-validateMessage: content.
//   - x-aggrega-selector:  pass-through metadata consumed elsewhere
//     (the catalog selector resolution machinery).
//   - x-aggrega-inject:    pass-through metadata consumed elsewhere
//     (the inject-mechanism resolution machinery).
//
// The wrapper that the compact-form compiler emits for non-trivial when:
// cases (if: true + then: + optional else: + x-aggrega-when:) is
// vestigial under this adapter — the adapter never reads if:/then:/
// else: directly when x-aggrega-when: is present. The wrapper exists
// only as a degraded-but-not-wrong fallback for standard JSON Schema
// 2020-12 consumers that do not recognise x-aggrega-when:.
//
// The Validate entry point takes three arguments: the schema, the
// instance, and a Context carrying the runtime scope template predicates
// evaluate against (the Aggregate's resolved parameter map plus the
// surrounding object scope). This is the one place this validator
// departs from the standard "schema validates instance" model — Go
// template predicates require an evaluation scope, and the adapter
// surfaces it through the Context type explicitly.
//
// The adapter is intended for short-lived process use (CLI invocation;
// per-build resolution). Long-lived service consumers (catalog publishing
// pipelines, hub UI live validation) should refresh the adapter
// periodically or wrap it with a bounded LRU cache to avoid unbounded
// memory growth from the *Schema → *Resolved cache.
//
// # Reproducible reports
//
// Two runs of the same Validate call over the same instance produce
// byte-identical Reports. Both halves of that are load-bearing and they are
// separate properties: the same findings, in the same order.
//
// The set is the harder half. A JSON Schema library evaluates an object's
// declared properties, its key set and its combinator branches by iterating
// maps, and stops at the first failure it reaches — so where two values are
// equally at fault it reports one of them, and which one differs between
// runs. Sorting a report assembled that way fixes the order of whatever it
// happens to contain and leaves the contents varying. The adapter therefore
// reaches every position itself, in an order fixed by the instance rather
// than by a map, and asks the library only about positions where there is
// nothing left to choose between.
//
// The order is the rendered FieldPath, then Keyword, then Message, each
// compared as text. Two consequences follow from "as text" and are stated
// because a reader would otherwise have to infer them from one observation:
// an array index orders lexically rather than numerically, so a list long
// enough to reach ten places reports element 10 before element 2; and a
// property name containing a character below the path separator sorts ahead
// of a deeper path under a shorter name.
//
// # What this adapter is the authority for
//
// The adapter is the authoritative enforcer of the keywords below, meaning
// it evaluates them itself, reports each violation at the path of the value
// that violates, and reports every one of several sibling violations rather
// than an arbitrary one:
//
//   - The declared-property families: properties, patternProperties,
//     additionalProperties in both its schema and its closed form, required
//     and dependentSchemas.
//   - The combinators: allOf, anyOf, oneOf and not.
//   - The array keywords: prefixItems, items, contains and the contains
//     count bounds.
//   - The key constraints: propertyNames and the property-count bounds.
//   - The conditional applicator if/then/else, except at a node carrying
//     x-aggrega-when:, where the predicate governs instead.
//   - The Aggrega extension keywords described above.
//   - The format keyword, which the library records and does not evaluate.
//     The names it may carry are the closed set package stringformat supports,
//     and New refuses a schema naming any other, wherever in the document the
//     name sits and whether or not an instance reaches it. A string value is
//     checked against its position's format beside every other constraint
//     there, so inside a combinator branch a format failure is a reason the
//     branch does not match, as any other failure is; a value that is not a
//     string passes, its type being its type keyword's to judge.
//
// A combinator branch is checked against the type its own shape requires
// before any of that shape is walked. A branch declaring properties,
// patternProperties, additionalProperties or dependentSchemas requires an
// object; given an instance that is not one, the adapter reports a type
// mismatch at that position and goes no further into the branch. Without
// that check each per-construct walk finds nothing to do — a non-object
// carries no keys to dispatch on — and the branch accepts a value its
// shape plainly refuses: a oneOf of a scalar form and an object form then
// matches on both branches, and the one real type mismatch surfaces as a
// spurious multiple-match at the combinator instead of at the value.
//
// Everything else — every other scalar constraint, so type, enum, const, the
// numeric and string bounds, dependentRequired — is evaluated by the
// underlying library at the single position that declares it. Those carry no
// ordering hazard: one position, one constraint, one message, with no map
// iterated to produce it.
//
// Where the library evaluates a whole subtree on the walk's behalf — the
// schema a reference names where the reference carries a constraint of its own
// beside it and so cannot stand in for it, the then: branch of a node carrying
// x-aggrega-when: under the always-true if: the compact form emits, the else:
// branch of such a node once its predicate is false, and a position that cannot
// be stood up on its own (below) — format is still checked, by a sweep of that
// subtree for it.
// The sweep follows only the applicators that apply unconditionally: declared,
// pattern and additional properties, array elements, allOf and in-document
// references. Inside anyOf, oneOf, not, if/then/else, dependentSchemas,
// contains or a nested x-aggrega-when: within such a subtree it does not
// descend, because deciding which branch applies there takes the evaluation
// the library performs; so a format there is not checked, and the sweep
// never refuses a value no constraint applies to.
//
// # What it is not the authority for
//
// Two exclusions, both deliberate, and neither of them silent in the sense
// that matters — each is stated here rather than left to be discovered from
// behaviour.
//
// unevaluatedProperties and unevaluatedItems are enforced only where what
// counts as already-reached is computable from the position that declares
// them. Their meaning is assembled from what the neighbouring keywords
// matched, so a position that also carries a reference, a combinator, a
// conditional or a dependent schema puts part of that set out of reach: the
// applicator contributes matches this position cannot see. There the
// keyword is not enforced. The alternative is worse than under-reporting —
// a set computed too small refuses values another applicator had in fact
// accepted, which is a diagnostic for a mistake the author did not make.
// unevaluatedItems is likewise not enforced beside contains, for the same
// reason. No schema shipped with the compiler relies on either keyword.
//
// A position the adapter cannot resolve on its own is enforced, but not by
// the walk, and what it loses is reporting precision rather than the
// constraint — format included, which the sweep described above checks
// there within the bound stated with it. Such a position is reached by a reference the adapter cannot
// resolve standalone — a pointer into a declared property rather than into a
// reusable definition, a pointer into another document, or a definition whose
// own references reach outside the definitions in scope — because standing a
// position up carries the definitions in scope onto it and nothing else, so
// only a name among those definitions still resolves there.
//
// The constraints at such a position are checked against the whole document
// instead, which is the one scope the reference does resolve in, and the
// failures the library reports there are kept when the position they name is
// at or below one of these positions and dropped otherwise. Dropping the rest
// is what keeps the check from reporting a second time on everything the walk
// already covered; it cannot suppress a first report either, because a
// position that cannot be resolved is not descended into, so its whole
// subtree is the walk's alone to miss.
//
// Two costs, both stated because neither is visible from a passing document.
// The library stops at the first failure it finds, so a document at fault in
// several of these positions is reported for one of them and the choice is
// the library's; a document at fault in only one is always reported for it,
// which is the property that stops such a document validating clean. And the
// failure is named at the position the library's own location chain reached
// before the reference moved it, so it lands on the position carrying the
// reference rather than on the exact value beneath it. Prefer a reference
// into a reusable definition, which resolves and is reported in full, at the
// path of the value at fault. TestNoShippedSchemaCarriesAPositionThatCannotBeStoodUp
// pins the count of such positions at zero for every schema shipped with the
// compiler.
package jsonschema
