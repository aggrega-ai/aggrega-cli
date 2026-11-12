// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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
func ErrSchemaResolve() error { return errSchemaResolve }

var errSchemaResolve = cerrors.Register("schema-not-resolvable", cerrors.Meta{
	ShortCode: "E0218",
	Message:   "validator/jsonschema: schema resolution failed",
	RulePlace: cerrors.NotARule("A schema could not be resolved when the validating adapter was constructed, so nothing was " +
		"built to validate against. It reports a resolution that did not yield a schema rather than a " +
		"rule refusing content."),
})

// ErrExtensionEval indicates an x-aggrega-when: or x-aggrega-
// validate: template predicate failed to evaluate (template parse
// error, evaluation runtime error, or non-boolean result).
func ErrExtensionEval() error { return errExtensionEval }

var errExtensionEval = cerrors.Register("schema-extension-evaluation-failed", cerrors.Meta{
	ShortCode: "E0216",
	Message:   "validator/jsonschema: x-aggrega extension evaluation failed",
	RulePlace: cerrors.NotARule("An extension template predicate could not be evaluated to a boolean — a parse error, a " +
		"runtime error, or a result that is not boolean. It reports that answering the question " +
		"became impossible rather than a rule that refused content."),
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

	// Err is the underlying sentinel: ErrSchemaResolve where the
	// schema itself could not be resolved, or ErrExtensionEval where
	// an x-aggrega template predicate failed to evaluate. Those are
	// the two conditions the adapter raises; a failure of the
	// instance against its schema is not among them, because such a
	// failure is reported as a Finding on *Report rather than as an
	// error.
	Err error
}

// Error opens with the condition's registered sentence and names the
// occurrence after it: the offending path, the offending keyword, and the
// adapter-side description of the failure. Never returns a raw library-error
// string.
//
// The sentence is read off the condition rather than written here, so the words
// a reader sees have one home. Note that these conditions register their
// sentences with a package label already in them, and that label is therefore
// printed by the sentence rather than by this composer, which writes none of
// its own. Two labels in one line is what that avoids.
func (e *AdapterError) Error() string {
	values := make([]string, 0, 3)
	if e.Path != "" {
		values = append(values, "at "+e.Path)
	}
	if e.Keyword != "" {
		values = append(values, fmt.Sprintf("keyword %q", e.Keyword))
	}
	if e.Message != "" {
		values = append(values, e.Message)
	}
	return cerrors.ComposeMessage(e.Err, values, "")
}

// Unwrap returns the underlying sentinel for errors.Is matching.
func (e *AdapterError) Unwrap() error {
	return e.Err
}
