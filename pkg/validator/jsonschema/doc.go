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
// Validation findings sort by FieldPath → Keyword → Message before
// emission, guaranteeing byte-identical Reports across two runs of the
// same Validate call regardless of library iteration order.
package jsonschema
