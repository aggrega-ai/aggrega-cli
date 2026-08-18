// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package errors

import (
	stderrors "errors"
	"fmt"
	"strings"
)

// Category names which authoring artifact is at fault for a given
// CompilerError. The six values partition the compiler's error surface
// by who must act to resolve the failure: developer (Aggregate), operator
// (Release), catalog author (Recipe / Component / Feature / FeatureRecipe
// / Manifest), provider engine (translation cannot express the requested
// output), the remote registry / network layer (a fetch of a pinned
// artifact failed at lock or build time), or an operator-attached
// policy plugin (a governance verdict rejected the build — the
// fix is to satisfy the enforced policy or reconsider its attachment,
// not to edit an authoring file).
type Category string

// The six error categories.
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
	// PolicyError surfaces a governance verdict from an operator-attached
	// policy plugin: the plugin evaluated the assembled IR graph
	// and rejected the build against a rule it enforces. Distinct from
	// DeveloperError and CatalogError because the rejection is neither a
	// developer authoring mistake on the Aggregate nor a catalog/ops
	// authoring fault — it is a policy the deployment must satisfy. The
	// remediation is to bring the input into compliance with the enforced
	// policy or to reconsider the policy attachment, not to correct an
	// authoring error in the Aggregate.
	PolicyError Category = "POLICY_ERROR"
)

// SourceLocation points at the most specific authoring locator available for
// the failure, which is a file path or a JSON path depending on what the
// surface that reported it can supply.
//
// File carries that locator. It is a real filesystem path where the failure
// has a position in an authored document — the typical shape for a catalog
// content error surfacing a YAML position. Where no file position applies it
// is a logical identifier the reader can still act on: an entity name, a
// Release name, a graph node id, or a JSON-path field locator such as
// ".values.port". The dual meaning is deliberate; a caller that has only a
// field locator should pass it rather than leave the pointer empty.
//
// Locator is for the caller that has BOTH halves: a document in File and the
// authoring key within that document. It is a dotted key such as
// ".spec.components.checkout.uses.databases", never a line or a column. Where
// only one half is in hand there is nothing to separate and File carries it, so
// this field stays empty and the rendering is unchanged; it exists because
// folding a key into File would displace the path a reader opens the file with,
// and a single string cannot be both without one of them losing.
//
// Line and Column are 1-based positions when available and zero when the
// pointer is file-level only, which is always the case for a logical
// identifier.
type SourceLocation struct {
	File    string
	Locator string
	Line    int
	Column  int
}

// String formats the source location for human consumption: "path:line:col"
// when both Line and Column are non-zero, "path:line" when only Line is set,
// "path" when both are zero.
//
// A non-empty Locator is appended to that position after the word "at", so the
// pair reads as one position and a reader can see where the document ends and
// the key begins. The two are joined by a word rather than run together because
// a locator opens with a separator of its own, and concatenating them yields one
// string that reads as a single file name with a nonsense extension. Where the
// position is empty the locator stands alone rather than being prefixed by a
// separator with nothing before it.
//
// This is the one place a position is composed, so every surface that names one
// names it in the same shape. An empty Locator returns exactly what the file,
// line and column alone produce, which is what keeps a caller that supplies no
// locator rendering byte-identically.
func (s SourceLocation) String() string {
	var position string
	switch {
	case s.Line > 0 && s.Column > 0:
		position = fmt.Sprintf("%s:%d:%d", s.File, s.Line, s.Column)
	case s.Line > 0:
		position = fmt.Sprintf("%s:%d", s.File, s.Line)
	default:
		position = s.File
	}
	switch {
	case s.Locator == "":
		return position
	case position == "":
		return s.Locator
	default:
		return position + " at " + s.Locator
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
//
// Staying on one line is deliberate rather than incidental. An error is
// routinely re-wrapped on its way up with fmt.Errorf("%w: ..."), and a
// multi-line Error would carry its own layout into every such wrap, so the
// rich shape would leak into contexts that expect one line per error. The
// multi-line rendering therefore belongs to the formatter, which knows it is
// writing to a terminal, and not to this method, which does not.
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

// CauseChain renders err together with every cause beneath it as ONE line,
// each segment joined by ": ". It exists because Error above deliberately
// renders a single frame: that keeps the rich multi-line CLI rendering
// readable, but it means a caller composing a one-line note out of err.Error()
// alone reports the outermost description and silently drops the specific
// diagnostic underneath — the frame that names the file's actual fault.
// CauseChain is the flattened form for those single-line contexts.
//
// A segment whose text the accumulated output already contains is skipped, so
// a frame that only re-states its cause is not said twice. Returns the empty
// string for a nil error.
func CauseChain(err error) string {
	if err == nil {
		return ""
	}
	var segments []string
	for cur := err; cur != nil; cur = stderrors.Unwrap(cur) {
		txt := cur.Error()
		if txt == "" {
			continue
		}
		redundant := false
		for _, prior := range segments {
			if strings.Contains(prior, txt) {
				redundant = true
				break
			}
		}
		if !redundant {
			segments = append(segments, txt)
		}
	}
	return strings.Join(segments, ": ")
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
