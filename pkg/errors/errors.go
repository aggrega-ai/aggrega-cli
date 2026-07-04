// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package errors

import "fmt"

// Category names which authoring artifact is at fault for a given
// CompilerError. The five values partition the compiler's error surface
// by who edits the offending file: developer (Aggregate), operator
// (Release), catalog author (Recipe / Component / Feature / FeatureRecipe
// / Manifest), provider engine (translation cannot express
// the requested output), or the remote registry / network layer (a
// fetch of a pinned artifact failed at lock or build time).
type Category string

// The five error categories.
const (
	DeveloperError   Category = "DEVELOPER_ERROR"
	OpsError         Category = "OPS_ERROR"
	CatalogError     Category = "CATALOG_ERROR"
	TranslationError Category = "TRANSLATION_ERROR"
	// FetchError surfaces failures from the remote-acquisition layer
	// (registry fetch, network transport, content-digest verification)
	// at both lock time (`aggrega pull`) and build time (cache read
	// with integrity verification). Distinct from CatalogError because
	// the catalog author has no edit that fixes a 502 from a remote
	// registry; the remediation is operational (auth setup, network
	// connectivity, lockfile regeneration) rather than authoring.
	FetchError Category = "FETCH_ERROR"
)

// SourceLocation points at a position in the authoring artifact at fault.
// File is the path; Line and Column are 1-based positions when available,
// zero when the source pointer is file-level only.
type SourceLocation struct {
	File   string
	Line   int
	Column int
}

// String formats the source location for human consumption: "path:line:col"
// when both Line and Column are non-zero, "path:line" when only Line is set,
// "path" when both are zero.
func (s SourceLocation) String() string {
	switch {
	case s.Line > 0 && s.Column > 0:
		return fmt.Sprintf("%s:%d:%d", s.File, s.Line, s.Column)
	case s.Line > 0:
		return fmt.Sprintf("%s:%d", s.File, s.Line)
	default:
		return s.File
	}
}

// CompilerError is the compiler's categorised-error type. A CompilerError
// surfaces at CLI output through the build command's error path; the CLI
// formatter renders it in the documented shape (category tag in brackets,
// source pointer, message, optional template excerpt, optional pillar
// snapshot).
//
// Wrap is the underlying error chain (wrapped via fmt.Errorf("%w", ...) or
// equivalent). Unwrap returns Wrap so errors.Is and errors.As traverse the
// chain transparently.
type CompilerError struct {
	Category   Category
	Source     SourceLocation
	Message    string
	Expression string
	Context    map[string]any
	Wrap       error
}

// Error returns a single-line representation of the error suitable for
// log output. The CLI formatter renders the rich multi-line shape; this
// method is the fallback for non-CLI callers (tests, internal logs).
func (e *CompilerError) Error() string {
	if e == nil {
		return "<nil>"
	}
	src := e.Source.String()
	if src == "" {
		return fmt.Sprintf("[%s] %s", e.Category, e.Message)
	}
	return fmt.Sprintf("[%s] %s: %s", e.Category, src, e.Message)
}

// Unwrap returns the wrapped inner error so errors.Is and errors.As
// traverse the chain. Returns nil when no inner error is carried.
func (e *CompilerError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Wrap
}

// New constructs a CompilerError. Pass an empty SourceLocation when the
// error is not attributable to a specific file/position; pass nil for
// context when no pillar snapshot is available; pass nil for wrap when
// the error originates here.
func New(category Category, source SourceLocation, message string, context map[string]any, wrap error) *CompilerError {
	return &CompilerError{
		Category: category,
		Source:   source,
		Message:  message,
		Context:  context,
		Wrap:     wrap,
	}
}

// Wrap re-categorises an existing error as a CompilerError with the given
// Category, preserving the inner error chain. Useful at category-boundary
// crossings where an error from one layer (e.g., a parse error originating
// in a Recipe) must surface to the operator under the Catalog category
// rather than its origin layer's category.
func Wrap(err error, category Category, source SourceLocation, message string) *CompilerError {
	return &CompilerError{
		Category: category,
		Source:   source,
		Message:  message,
		Wrap:     err,
	}
}
