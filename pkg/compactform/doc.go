// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compactform compiles the YAML-shaped declarative shorthand for
// JSON Schema 2020-12 content into a resolved JSON Schema document.
// The compact form is the recommended authoring surface for schema-bearing
// fields (Component / Feature `schema:`, Aggregate `parameters:`,
// SchemaLibrary entries); JSON Schema YAML is a co-equal authoring option
// that the same Compile entry point accepts directly. Mixing the two forms
// within one document is supported via per-node dispatch on content shape.
//
// The vocabulary is small (about 15 keywords): type keywords (type,
// description, default, required), constraint keywords (enum, min, max,
// pattern, format, minLength, maxLength), composition keywords (allOf,
// anyOf, oneOf, patternProperties, valuesType), container keywords
// (items, properties, additionalProperties), and Aggrega-specific
// additions (validate, validateMessage, when, sensitive, $jsonschema,
// $mode).
// Each keyword maps mechanically to JSON Schema 2020-12; the compiler
// transforms compact form to JSON Schema before any validation runs.
//
// type: accepts either a single type name (type: string) or a sequence of
// permitted names (type: [string, integer, boolean] — the JSON Schema
// 2020-12 array-of-types form), and a sequence is carried into the
// compiled schema as authored. Any other shape at type: is refused at
// compile time with a diagnostic naming the position, rather than
// tolerated or left to fail a type assertion. min: and max: compile to a
// numeric, string or array bound chosen by the declared type, so they
// require type: to resolve to exactly one name; beside a sequence — or
// with no type: at all — they halt, with a message steering the author to
// narrow type: or drop the constraint.
//
// The escape hatch $jsonschema: embeds full JSON Schema YAML at any node
// for cases the compact form cannot express. It widens the STRUCTURE a node
// may carry and not the vocabulary: the body's keys are checked against the
// same known-keyword set as any other node, so an unrecognised key inside an
// escape is refused rather than carried through. The $mode: annotation
// (compact / jsonschema) provides explicit dispatch for the rare edge
// cases where content-shape dispatch is ambiguous.
//
// A cross-document $ref: naming a SchemaLibrary entry is resolved and
// inlined at compile time, and every property the entry contributes
// carries an x-aggrega-source annotation naming the entry it came from.
// That annotation names the entry the consumer referenced, never the one a
// property ultimately originated in: where a referenced entry itself
// references a second library, the outer expansion re-annotates everything
// the inner one contributed, so attribution is exactly one step deep.
//
// Compile is a pure function over its input. Same input produces byte-
// identical output across runs (the deterministic-output invariant
// applies); iteration over map-typed input follows author order via
// the OrderedMap[V] substrate at pkg/types.
//
// The runtime evaluation of validate: + when: Go-template predicates is
// performed by the validator adapter at pkg/validator/jsonschema/; this
// package emits the template body into x-aggrega-validate: / x-aggrega-when:
// extension keywords for the adapter to consume.
package compactform
