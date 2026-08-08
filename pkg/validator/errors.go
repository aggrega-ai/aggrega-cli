// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// Sentinel errors for errors.Is matching.

const ErrInheritanceMerge cerrors.Code = "validator/inheritance-merge"

var _ = cerrors.Register(ErrInheritanceMerge, cerrors.Meta{
	Message: "inheritance merge failed: resolve the conflicting field definitions across the extends chain",
})

const ErrCyclicExtension cerrors.Code = "validator/cyclic-extension"

var _ = cerrors.Register(ErrCyclicExtension, cerrors.Meta{
	Message: "cyclic extends chain",
})

const ErrAssertionNameCollision cerrors.Code = "validator/assertion-name-collision"

var _ = cerrors.Register(ErrAssertionNameCollision, cerrors.Meta{
	Message: "assertion name declared twice across the extends chain: rename the extending entity's assertion, whose assertions add to the base's and never replace one",
})

// ValidationDetail describes a validation failure: where it was found, what
// was required there, what was found instead, what to do about it, and the
// sentinel callers match on. Every field is optional — what identifies the
// failure is the entity kind and name given to NewValidationError, not
// anything here.
type ValidationDetail struct {
	FieldPath  string // e.g. ".values.port"
	Constraint string // e.g. "must be >= 1024"
	Actual     string // e.g. "80"
	Suggestion string // e.g. "Use a port number >= 1024"
	Err        error  // sentinel, matched via errors.Is
}

// ValidationError is a structured validation failure: which entity is at
// fault, where in it, what was required, and what to do about it. Raw
// validator-library error strings are never exposed — they are wrapped
// internally.
//
// Its fields are unexported, so NewValidationError is the only way to
// populate one and no caller can describe a failure without saying which kind
// of entity it is against. That kind does two jobs and both follow from the
// one value: it selects the authoring domain the failure is reported under,
// and it supplies the leading word of the entity reference the message
// renders. Neither is inferred from the rendered text.
type ValidationError struct {
	kind   envelope.Kind
	name   string
	detail ValidationDetail
}

// categoryFor resolves kind to the authoring domain a failure on it is
// reported under, and refuses a kind that belongs to none — the zero kind
// included.
//
// It is the single site of that refusal, and it runs both where a failure is
// built and where it is converted. Construction is the useful place to fail,
// because the call site that supplied the kind is still on the stack; the
// conversion check is what makes the refusal hold anyway for a value that
// never went through the constructor, since an empty composite literal of
// this type is legal from anywhere and would otherwise convert to a failure
// carrying no domain at all.
func categoryFor(kind envelope.Kind) cerrors.Category {
	category, ok := cerrors.ClassifyEntityKind(string(kind))
	if !ok {
		panic(fmt.Sprintf("validator: entity kind %q belongs to no authoring domain, so a validation "+
			"failure on it cannot be attributed; pass a kind the entity-kind category mapping covers, "+
			"or extend that mapping to cover this one", kind))
	}
	return category
}

// NewValidationError builds a validation failure against the named entity of
// the given kind. name is the entity's metadata name, e.g.
// "core/api-server:1.0.0"; kind is the entity kind that name belongs to.
//
// It panics when kind names no authoring domain — the zero kind included.
// A kind reaches here only as a constant chosen where the failure is
// reported, never from an authored document, so an unmappable one means
// either a call site left the kind unset or an entity kind has begun
// carrying categorised failures without the kind-to-domain mapping being
// extended to it. Both are mistakes in the compiler rather than in anything
// a user wrote, and both would otherwise surface as a failure quietly
// attributed to the wrong authoring domain.
func NewValidationError(kind envelope.Kind, name string, detail ValidationDetail) *ValidationError {
	// The domain is resolved and discarded here purely to refuse an unmappable
	// kind while the site that supplied it is still on the stack. The value is
	// resolved again at conversion, from the kind, so nothing derived is stored.
	categoryFor(kind)
	return &ValidationError{kind: kind, name: name, detail: detail}
}

// entity renders the entity reference the failure is reported against, in
// the "<Kind> <name>" shape — e.g. "Component core/api-server:1.0.0".
func (e *ValidationError) entity() string {
	return string(e.kind) + " " + e.name
}

// message renders the one-line description shared by Error and the compiler
// error ToCompilerError produces. Both surfaces report the same failure, so
// there is one rendering rather than two that agree until one is edited.
func (e *ValidationError) message() string {
	msg := fmt.Sprintf("%s: validation error at %s", e.entity(), e.detail.FieldPath)
	if e.detail.Constraint != "" {
		msg += fmt.Sprintf(" — %s", e.detail.Constraint)
	}
	if e.detail.Actual != "" {
		msg += fmt.Sprintf(" (got %s)", e.detail.Actual)
	}
	if e.detail.Suggestion != "" {
		msg += fmt.Sprintf(". %s", e.detail.Suggestion)
	}
	return msg
}

// Error returns a formatted human-readable message. Never contains raw validator-library output.
func (e *ValidationError) Error() string {
	return e.message()
}

// Unwrap returns the sentinel error for errors.Is matching.
func (e *ValidationError) Unwrap() error {
	return e.detail.Err
}

// ToCompilerError converts the structured ValidationError into a
// *cerrors.CompilerError.
//
// The Category is the authoring domain the failure's entity kind belongs to:
// a Component, Recipe, Feature, FeatureRecipe or Manifest is catalog content,
// an Aggregate is the developer's surface, a Release is the operator's. It is
// read from the kind the failure carries, with no default to fall through to
// — a kind belonging to no domain is refused rather than substituted for.
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
	source := cerrors.SourceLocation{File: e.detail.FieldPath}
	return cerrors.Wrap(e, categoryFor(e.kind), source, e.message())
}
