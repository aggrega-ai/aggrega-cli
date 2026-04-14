// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"errors"
	"fmt"
)

// Sentinel errors for errors.Is matching.
var (
	ErrSchemaValidation      = errors.New("schema validation failed")
	ErrInheritanceMerge      = errors.New("inheritance merge failed")
	ErrUsesSchemaUnification = errors.New("usesSchema unification failed")
)

// ValidationError is a structured validation failure with entity context, field path,
// constraint description, actual value, and a human-readable suggestion.
// Raw CUE error strings are never exposed — they are wrapped internally.
type ValidationError struct {
	Entity     string // e.g. "Component core/api-server:1.0.0"
	FieldPath  string // e.g. ".values.port"
	Constraint string // e.g. "must be >= 1024"
	Actual     string // e.g. "80"
	Suggestion string // e.g. "Use a port number >= 1024"
	Err        error  // sentinel (ErrSchemaValidation | ErrInheritanceMerge | ErrUsesSchemaUnification)
}

// Error returns a formatted human-readable message. Never contains raw CUE output.
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
