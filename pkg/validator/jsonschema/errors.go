// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import (
	"fmt"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// Sentinel errors for errors.Is matching against adapter failures.

// ErrSchemaResolve indicates the underlying library failed to
// resolve the schema (typically a malformed $ref or a meta-schema
// validation failure). Surfaces at adapter construction time
// (eager pre-resolution).
const ErrSchemaResolve cerrors.Code = "validator-jsonschema/schema-resolve"

var _ = cerrors.Register(ErrSchemaResolve, cerrors.Meta{
	Message: "validator/jsonschema: schema resolution failed",
})

// ErrInstanceValidation indicates the underlying library reported
// a validation failure against the instance that the adapter could
// not translate to a structured Finding. Findings produced from
// validation failures travel via *Report; this sentinel covers the
// envelope-level failure where the library returns an error the
// adapter cannot decompose.
const ErrInstanceValidation cerrors.Code = "validator-jsonschema/instance-validation"

var _ = cerrors.Register(ErrInstanceValidation, cerrors.Meta{
	Message: "validator/jsonschema: instance validation failed",
})

// ErrExtensionEval indicates an x-aggrega-when: or x-aggrega-
// validate: template predicate failed to evaluate (template parse
// error, evaluation runtime error, or non-boolean result).
const ErrExtensionEval cerrors.Code = "validator-jsonschema/extension-eval"

var _ = cerrors.Register(ErrExtensionEval, cerrors.Meta{
	Message: "validator/jsonschema: x-aggrega extension evaluation failed",
})

// AdapterError wraps the underlying library or template evaluation
// error with adapter-side context. Callers match the sentinel via
// errors.Is and access fields directly for the path / keyword / message.
type AdapterError struct {
	// Path is the YAML-path-like location of the offending node
	// (e.g. "spec.schema.values.port"). Empty when the failure is at
	// the root or pre-walk.
	Path string

	// Keyword is the offending JSON Schema or x-aggrega keyword.
	// Empty when the failure is not keyword-specific.
	Keyword string

	// Message is the human-readable description of the failure.
	Message string

	// Err is the underlying sentinel (one of ErrSchemaResolve /
	// ErrInstanceValidation / ErrExtensionEval).
	Err error
}

// Error returns a formatted human-readable message naming the
// offending path and keyword. Never returns a raw library-error
// string.
func (e *AdapterError) Error() string {
	prefix := "validator/jsonschema"
	if e.Path != "" {
		prefix += " at " + e.Path
	}
	if e.Keyword != "" {
		prefix += fmt.Sprintf(" (keyword %q)", e.Keyword)
	}
	return prefix + ": " + e.Message
}

// Unwrap returns the underlying sentinel for errors.Is matching.
func (e *AdapterError) Unwrap() error {
	return e.Err
}
