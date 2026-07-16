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
package errors
