// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package errors provides the compiler's categorised-error surface for
// CLI consumption.
//
// CompilerError carries a Category, a SourceLocation pointing at the
// authoring artifact at fault, a human-facing Message, an optional
// Expression (the failing template expression where applicable), and a
// Context map (the per-pass pillar snapshot at the failure point).
//
// Six categories partition the error surface by origin — four name the
// authoring artifact the operator must edit to fix the error, and two name
// non-authoring faults:
//   - DeveloperError: the Aggregate is at fault.
//   - OpsError: the Release is at fault.
//   - CatalogError: a Recipe, Component, Feature, FeatureRecipe, Plugin,
//     or Manifest is at fault.
//   - TranslationError: the provider engine cannot express the requested
//     synthesised output (no authoring fix; the developer or operator
//     must change input shape).
//   - FetchError: a remote-acquisition fault (registry or source-artifact
//     fetch); the remediation is operational, not authoring.
//   - PolicyError: an operator-attached policy plugin rejected the
//     build; the fix is to satisfy the enforced policy or reconsider its
//     attachment, not to edit an authoring artifact.
//
// CompilerError implements the standard error interface plus Unwrap so
// errors.Is and errors.As work against the carried inner error chain.
//
// # Identities and occurrences
//
// A halting condition has an identity and an occurrence, and this package
// carries both.
//
// The identity is a value declared by a Register call and published by an
// exported accessor standing beside it. It satisfies the error interface, it
// stays matchable under errors.Is through arbitrary wrapping, and one such
// value is shared by every occurrence of the condition it names. Neither its
// type nor its fields are exported, so no package but the one that declares it
// can produce it.
//
// The code the identity carries is a separate thing from the identity itself,
// and the separation is deliberate. The identity is what Go code matches; the
// code is the stable identifier a consumer outside Go reads, obtained from any
// error in the chain with CodeOf. It is an output, never an input to matching —
// writing the string back yields a string, not an identity.
//
// CompilerError is the occurrence: it carries the Category, the SourceLocation
// and the surrounding detail that differ between one failure and the next.
//
// The division follows from that sharing. Anything that differs between two
// occurrences of the same condition belongs on the instance, because a single
// shared value cannot express it; anything true of the condition itself belongs
// beside the identity, declared once.
package errors
