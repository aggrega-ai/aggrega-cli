// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compactform

import (
	"fmt"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/refcycle"
)

// Sentinel errors for errors.Is matching against compile failures.

// ErrUnknownKeyword indicates a node carried a key that is neither a
// compact-form keyword nor a JSON-Schema-reserved keyword nor a
// recognised escape (`$jsonschema`, `$mode`, `$ref`). The compiler
// rejects unknown keywords up front so typos surface immediately.
func ErrUnknownKeyword() error { return errUnknownKeyword }

var errUnknownKeyword = cerrors.Register("compactform/unknown-keyword", cerrors.Meta{
	Message: "compactform: unknown keyword",
})

// ErrAmbiguousDispatch indicates a node's content shape could be
// interpreted as either compact form or JSON Schema and the author
// did not supply a `$mode:` annotation to disambiguate. The canonical
// example is a compact-form schema with a field literally named
// `type`.
func ErrAmbiguousDispatch() error { return errAmbiguousDispatch }

var errAmbiguousDispatch = cerrors.Register("compactform/ambiguous-dispatch", cerrors.Meta{
	Message: "compactform: ambiguous dispatch — $mode required",
})

// ErrEscapeContentInvalid indicates a `$jsonschema:` block carried
// content that does not parse as JSON Schema YAML. The escape hatch
// embeds JSON Schema verbatim; malformed content is rejected here
// rather than passed downstream to the validator.
func ErrEscapeContentInvalid() error { return errEscapeContentInvalid }

var errEscapeContentInvalid = cerrors.Register("compactform/escape-content-invalid", cerrors.Meta{
	Message: "compactform: $jsonschema content invalid",
})

// ErrConstraintTypeMismatch indicates a constraint keyword was applied
// to a field whose declared type does not accept the constraint
// (e.g. `min:` on a `type: object` field, `pattern:` on a numeric
// type). The dispatch of `min` / `max` to JSON Schema's
// `minimum` / `minLength` / `minItems` depends on the field's type.
func ErrConstraintTypeMismatch() error { return errConstraintTypeMismatch }

var errConstraintTypeMismatch = cerrors.Register("compactform/constraint-type-mismatch", cerrors.Meta{
	Message: "compactform: constraint not valid for declared type",
})

// ErrReferenceUnresolvable indicates a `$ref:` URL is syntactically
// malformed (the resolver itself runs in pkg/validator/merger/ and
// pkg/validator/jsonschema/; the compactform compiler only validates
// the URL shape).
func ErrReferenceUnresolvable() error { return errReferenceUnresolvable }

var errReferenceUnresolvable = cerrors.Register("compactform/reference-unresolvable", cerrors.Meta{
	Message: "compactform: $ref unresolvable",
})

// ErrUnresolvedSchemaLibraryRef indicates a cross-document `$ref:`
// targets a SchemaLibrary entry that is not present in the catalog —
// either the library name (document portion of the URI) is unknown
// or the entry name (fragment portion) is absent from the library's
// schemas map. Distinct from ErrReferenceUnresolvable which signals
// a syntactically malformed URL; this sentinel signals a syntactically
// valid URL whose target does not exist.
func ErrUnresolvedSchemaLibraryRef() error { return errUnresolvedSchemaLibraryRef }

var errUnresolvedSchemaLibraryRef = cerrors.Register("compactform/unresolved-schema-library-ref", cerrors.Meta{
	Message: "compactform: cross-document $ref target not found",
})

// ErrCircularRef indicates a schema that cannot be expanded, because
// expanding it reproduces what the expansion started from. Two conditions
// report through it. They are detected by different means, and the message
// is what tells them apart: one names a reference chain, the other names an
// anchor.
//
// The first is a cross-document expansion cycle on the `$ref` and `$import`
// surfaces. The compactform expander maintains a resolution-stack of
// in-flight canonical keys during recursive expansion and surfaces this
// error with the chain named in the message (e.g.
// "A#/schemas/X -> B#/schemas/Y -> A#/schemas/X"). Deeper cycle graphs
// across multi-hop reference chains are detected by the same stack
// discipline.
//
// The second is a YAML anchor aliased somewhere inside its own content,
// which denotes an infinite tree within a single document. It is detected by
// node identity inside one parsed document rather than by canonical key
// across documents, and the rule and its message belong to pkg/anchorcycle
// — see that package for why the two detectors are deliberately not
// unified. Compile refuses it on the tree it is handed; the `$jsonschema`
// escape refuses it again on an escape body, which is a block-scalar string
// when Compile inspects its input and becomes a node tree only one parse
// later.
//
// It is the shared refcycle.ErrCycle sentinel, so every surface above
// reports one cycle identity rather than teaching an author several names
// for "this expands without end". Match with
// errors.Is(err, ErrCircularRef()) or, equivalently,
// errors.Is(err, refcycle.ErrCycle()).
func ErrCircularRef() error { return refcycle.ErrCycle() }

// ErrHostInLibraryRef indicates a cross-document `$ref:` carries a
// registry host — the `oci://<host>/…` form. A library reference
// addresses its target by identity (the bare-name form
// `<Library>#/schemas/<Entry>`), never by physical registry location:
// the library name resolves against the loaded closure and the version
// is supplied by the lockfile. A host in a reference is rejected at
// compile so physical location cannot leak into authored source; the
// error message steers the author to the identity form.
func ErrHostInLibraryRef() error { return errHostInLibraryRef }

var errHostInLibraryRef = cerrors.Register("compactform/host-in-library-ref", cerrors.Meta{
	Message: "compactform: cross-document $ref carries a registry host; reference the library by identity",
})

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

	// Err is the underlying sentinel — one of ErrUnknownKeyword /
	// ErrAmbiguousDispatch / ErrEscapeContentInvalid /
	// ErrConstraintTypeMismatch / ErrReferenceUnresolvable /
	// ErrUnresolvedSchemaLibraryRef / ErrCircularRef / ErrHostInLibraryRef,
	// which is the same set Compile documents.
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
