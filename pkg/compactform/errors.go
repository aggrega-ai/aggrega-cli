// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compactform

import (
	"errors"
	"fmt"
)

// Sentinel errors for errors.Is matching against compile failures.
var (
	// ErrUnknownKeyword indicates a node carried a key that is neither a
	// compact-form keyword nor a JSON-Schema-reserved keyword nor a
	// recognised escape (`$jsonschema`, `$mode`, `$ref`). The compiler
	// rejects unknown keywords up front so typos surface immediately.
	ErrUnknownKeyword = errors.New("compactform: unknown keyword")

	// ErrAmbiguousDispatch indicates a node's content shape could be
	// interpreted as either compact form or JSON Schema and the author
	// did not supply a `$mode:` annotation to disambiguate. The canonical
	// example is a compact-form schema with a field literally named
	// `type`.
	ErrAmbiguousDispatch = errors.New("compactform: ambiguous dispatch — $mode required")

	// ErrEscapeContentInvalid indicates a `$jsonschema:` block carried
	// content that does not parse as JSON Schema YAML. The escape hatch
	// embeds JSON Schema verbatim; malformed content is rejected here
	// rather than passed downstream to the validator.
	ErrEscapeContentInvalid = errors.New("compactform: $jsonschema content invalid")

	// ErrConstraintTypeMismatch indicates a constraint keyword was applied
	// to a field whose declared type does not accept the constraint
	// (e.g. `min:` on a `type: object` field, `pattern:` on a numeric
	// type). The dispatch of `min` / `max` to JSON Schema's
	// `minimum` / `minLength` / `minItems` depends on the field's type.
	ErrConstraintTypeMismatch = errors.New("compactform: constraint not valid for declared type")

	// ErrReferenceUnresolvable indicates a `$ref:` URL is syntactically
	// malformed (the resolver itself runs in pkg/validator/merger/ and
	// pkg/validator/jsonschema/; the compactform compiler only validates
	// the URL shape).
	ErrReferenceUnresolvable = errors.New("compactform: $ref unresolvable")
)

// CompileError is a structured compile failure carrying source-position
// provenance. The compiler emits CompileError wrapping a sentinel; callers
// match via errors.Is for the sentinel and access fields directly for the
// path / line / message.
type CompileError struct {
	// Path is the YAML-path-like location of the offending node (e.g.
	// "spec.schema.values.port"). Empty when the failure is at the root.
	Path string

	// Line is the 1-based line number in the source YAML file. Zero when
	// the source is not file-backed (e.g. a synthetic node from a test
	// fixture).
	Line int

	// Keyword is the offending compact-form or JSON Schema keyword.
	// Empty when the failure is not keyword-specific.
	Keyword string

	// Message is the human-readable description of the failure.
	Message string

	// Err is the underlying sentinel (one of ErrUnknownKeyword /
	// ErrAmbiguousDispatch / ErrEscapeContentInvalid /
	// ErrConstraintTypeMismatch / ErrReferenceUnresolvable).
	Err error
}

// Error returns a formatted human-readable message naming the offending
// path, line, and keyword. Never returns a raw library-error string.
func (e *CompileError) Error() string {
	prefix := "compactform"
	if e.Path != "" {
		prefix += " at " + e.Path
	}
	if e.Line > 0 {
		prefix += fmt.Sprintf(":%d", e.Line)
	}
	if e.Keyword != "" {
		prefix += fmt.Sprintf(" (keyword %q)", e.Keyword)
	}
	return prefix + ": " + e.Message
}

// Unwrap returns the underlying sentinel for errors.Is matching.
func (e *CompileError) Unwrap() error {
	return e.Err
}
