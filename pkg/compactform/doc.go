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
// The escape hatch $jsonschema: embeds full JSON Schema YAML at any node
// for cases the compact form cannot express. The $mode: annotation
// (compact / jsonschema) provides explicit dispatch for the rare edge
// cases where content-shape dispatch is ambiguous.
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
