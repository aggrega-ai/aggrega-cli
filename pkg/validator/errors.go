// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"errors"
	"fmt"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// Sentinel errors for errors.Is matching.
var (
	ErrSchemaValidation      = errors.New("schema validation failed: correct the flagged field to satisfy its declared schema")
	ErrInheritanceMerge      = errors.New("inheritance merge failed: resolve the conflicting field definitions across the extends chain")
	ErrCyclicExtension       = errors.New("cyclic extends chain")
	ErrUsesSchemaUnification = errors.New("usesSchema unification failed: reconcile the conflicting socket definitions across the extends chain")
)

// ValidationError is a structured validation failure with entity context, field path,
// constraint description, actual value, and a human-readable suggestion.
// Raw validator-library error strings are never exposed — they are wrapped internally.
type ValidationError struct {
	Entity     string // e.g. "Component core/api-server:1.0.0"
	FieldPath  string // e.g. ".values.port"
	Constraint string // e.g. "must be >= 1024"
	Actual     string // e.g. "80"
	Suggestion string // e.g. "Use a port number >= 1024"
	Err        error  // sentinel (ErrSchemaValidation | ErrInheritanceMerge | ErrUsesSchemaUnification)
}

// Error returns a formatted human-readable message. Never contains raw validator-library output.
func (e *ValidationError) Error() string {
	msg := fmt.Sprintf("%s: validation error at %s", e.Entity, e.FieldPath)
	if e.Constraint != "" {
		msg += fmt.Sprintf(" — %s", e.Constraint)
	}
	if e.Actual != "" {
		msg += fmt.Sprintf(" (got %s)", e.Actual)
	}
	if e.Suggestion != "" {
		msg += fmt.Sprintf(". %s", e.Suggestion)
	}
	return msg
}

// Unwrap returns the sentinel error for errors.Is matching.
func (e *ValidationError) Unwrap() error {
	return e.Err
}

// ToCompilerError converts the structured ValidationError into a
// *cerrors.CompilerError. The Category is classified by the Entity prefix
// per cerrors.ClassifyEntityCategory: "Component" / "Recipe" / "Feature" /
// "FeatureRecipe" / "Manifest" → CatalogError; "Aggregate" →
// DeveloperError; "Release" → OpsError; unknown → DeveloperError
// (conservative).
//
// The Source location maps the JSON-path FieldPath into the CompilerError's
// SourceLocation.File. FieldPath is a JSON-path-style locator (e.g.,
// ".values.port"), not a real file path; the dual semantics is the same
// as ResolverError.Field. The Message combines Constraint, Actual, and
// Suggestion preserving the structure that Error() carries.
//
// Inner error chain preserved via cerrors.Wrap so errors.Is against the
// validator's sentinel errors continues to match.
func (e *ValidationError) ToCompilerError() *cerrors.CompilerError {
	cat := cerrors.ClassifyEntityCategory(e.Entity)
	source := cerrors.SourceLocation{File: e.FieldPath}
	msg := fmt.Sprintf("%s: validation error at %s", e.Entity, e.FieldPath)
	if e.Constraint != "" {
		msg += " — " + e.Constraint
	}
	if e.Actual != "" {
		msg += fmt.Sprintf(" (got %s)", e.Actual)
	}
	if e.Suggestion != "" {
		msg += ". " + e.Suggestion
	}
	return cerrors.Wrap(e, cat, source, msg)
}
