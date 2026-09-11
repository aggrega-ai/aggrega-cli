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
// Keys authored beside such a $ref: compose into the referenced entry
// rather than being discarded: properties, required and description are
// admitted, any other key beside the reference ($mode: aside) is refused
// naming those three (ErrUnknownKeyword), and a properties: sibling that
// is not a mapping is refused where it is written
// (ErrEscapeContentInvalid). Consumer properties overwrite entry
// properties on key collision, and an overwritten property is
// consumer-authored rather than library-sourced, so it carries no
// x-aggrega-source annotation; required unions the two lists, entry
// first, deduplicated; a consumer description wins where one is present
// and the entry's survives where none is. The merged shape is closed —
// additionalProperties: false is injected whatever the entry declared —
// so an author who needs it to accept arbitrary keys composes the open
// object through the $jsonschema: escape rather than beside the reference.
//
// A library is addressed by identity and never by location: the reference
// carries the library name, and the version a build resolves to comes
// from the lockfile. A reference carrying a registry host (the oci://
// form) is refused at compile so a physical registry location cannot
// enter authored source, and where that URI parses the diagnostic names
// the identity-form reference to write instead (ErrHostInLibraryRef). A
// host-less :version is accepted rather than refused: it is stripped, the
// bare name resolves, and the asserted version is carried as a
// minimum-version floor for selection at lock time. The separator is :,
// matching Manifest mappings and the lockfile pin; an @ separator is
// refused with a steer to the : spelling rather than failing as an opaque
// library-not-found (ErrReferenceUnresolvable).
//
// Expansion is recursive — a referenced entry may reference another
// library — and the cycle guard keys on the version-stripped, host-less
// identity rather than on the reference as written, so one entry reached
// through two spellings is one key and a cycle cannot evade the guard by
// spelling its version inconsistently; a cycle halts naming the chain
// (ErrCircularRef). A syntactically valid reference whose target is
// absent halts as well: an unknown library name, an entry absent from a
// named library, and a cross-document reference compiled with no
// SchemaLibrary catalog in scope all report one unresolved-target
// condition (ErrUnresolvedSchemaLibraryRef), distinct from the
// malformed-shape condition a reference not of the form
// <LibraryName>#/schemas/<EntryName> raises (ErrReferenceUnresolvable).
//
// Compile is a pure function over its input. Same input produces
// byte-identical output across runs (the deterministic-output invariant
// applies); iteration over map-typed input follows author order via
// the OrderedMap[V] substrate at pkg/types.
//
// The runtime evaluation of validate: + when: Go-template predicates is
// performed by the validator adapter at pkg/validator/jsonschema/; this
// package emits the template body into x-aggrega-validate: / x-aggrega-when:
// extension keywords for the adapter to consume.
package compactform
