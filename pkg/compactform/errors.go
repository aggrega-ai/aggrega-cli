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

	// ErrUnresolvedSchemaLibraryRef indicates a cross-document `$ref:`
	// targets a SchemaLibrary entry that is not present in the catalog —
	// either the library name (document portion of the URI) is unknown
	// or the entry name (fragment portion) is absent from the library's
	// schemas map. Distinct from ErrReferenceUnresolvable which signals
	// a syntactically malformed URL; this sentinel signals a syntactically
	// valid URL whose target does not exist.
	ErrUnresolvedSchemaLibraryRef = errors.New("compactform: cross-document $ref target not found")

	// ErrCircularRef indicates a cross-document `$ref:` expansion cycle.
	// The compactform expander maintains a resolution-stack of in-flight
	// URIs during recursive expansion; a URI already on the stack triggers
	// this error with the chain named in the error message
	// (e.g. "A#/schemas/X -> B#/schemas/Y -> A#/schemas/X"). Basic two-
	// entry cycles are detected; deeper cycle graphs across multi-hop
	// reference chains are detected by the same stack discipline.
	ErrCircularRef = errors.New("compactform: circular cross-document $ref expansion")

	// ErrOCINotYetSupported indicates a cross-document `oci://` `$ref:`
	// names a SchemaLibrary that is NOT present in the local catalog — a
	// remote library that would have to be fetched at build. The `oci://`
	// `$ref` form itself resolves for workspace-local libraries (the
	// library is matched by name against the loaded catalog and its
	// `@version` is asserted); only fetch-at-build of a remote
	// (not-locally-present) library is deferred until the remote-library
	// fetch path ships.
	ErrOCINotYetSupported = errors.New("compactform: remote OCI SchemaLibrary resolution at build is not yet supported")

	// ErrSchemaLibraryVersionMismatch indicates a cross-document `oci://`
	// `$ref:` resolved its SchemaLibrary by name in the local catalog, but
	// the library's `metadata.version` does not equal the `@version` pinned
	// in the URI. Distinct from ErrUnresolvedSchemaLibraryRef (library name
	// unknown or entry absent): here both the library and the entry resolve,
	// but the version assertion fails — the remediation is to align the
	// URI's `@version` with the loaded library, not to fix the name. The
	// catalog's SchemaLibraries map is name-keyed (one version per name), so
	// `@version` is a validated assertion against the loaded library, not a
	// version selector.
	ErrSchemaLibraryVersionMismatch = errors.New("compactform: cross-document $ref version does not match the resolved SchemaLibrary")
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
