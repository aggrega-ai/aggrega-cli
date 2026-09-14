// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// Sentinel errors for errors.Is matching.

func ErrInheritanceMerge() error { return errInheritanceMerge }

var errInheritanceMerge = cerrors.Register("extends-merge-failed", cerrors.Meta{
	ShortCode: "E0205",
	Message:   "inheritance merge failed",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

func ErrCyclicExtension() error { return errCyclicExtension }

var errCyclicExtension = cerrors.Register("cyclic-extension", cerrors.Meta{
	ShortCode: "E0202",
	Message:   "cyclic extends chain",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrExtendsChainTooDeep fires when an extends chain terminates cleanly but
// runs past the depth bound. It is separate from ErrInheritanceMerge so that
// "this chain is longer than the bound allows" and "this chain names a parent
// the catalog does not hold" stay two answers a reader can tell apart — the
// remedies differ, and one identity covering both leaves a consumer matching on
// it unable to say which it caught.
//
// It is deliberately NOT named for the bound alone. The bare name would sit
// alongside a same-named condition in the topology walk, and a reader resolving
// either from the name would have no way to tell which walk reported.
func ErrExtendsChainTooDeep() error { return errExtendsChainTooDeep }

var errExtendsChainTooDeep = cerrors.Register("extends-chain-too-deep", cerrors.Meta{
	ShortCode: "E0220",
	Message:   "extends chain exceeds maximum depth",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

func ErrAssertionNameCollision() error { return errAssertionNameCollision }

var errAssertionNameCollision = cerrors.Register("assertion-name-collision", cerrors.Meta{
	ShortCode: "E0201",
	Message:   "assertion name declared twice across the extends chain",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrUnknownSchemaPillar fires when a Component or Feature writes a top-level
// key in its schema block that names none of the data pillars, so the content
// beneath that key is a contract nothing will ever read.
//
// The key set is closed for the same reason each pillar's own content is
// validated closed: the block enumerates a legal surface, so a key outside it
// is an authoring error rather than a permitted extension. Admitting one would
// leave the author believing a contract is in force while every consumer of the
// entity walks past it, which is the silent outcome a closed set removes.
//
// It is distinct from the condition raised when a pillar's authored content
// fails the schema that pillar declares. That one judges what is INSIDE a
// pillar; this one judges whether the key names a pillar at all, and the two
// are reached from different surfaces at different moments — this at entity
// load, the other once an instance's values are resolved against the contract.
//
// Category: CATALOG_ERROR — the fault is in an authored catalog document's
// schema block. Match with errors.Is(err, ErrUnknownSchemaPillar()).
func ErrUnknownSchemaPillar() error { return errUnknownSchemaPillar }

var errUnknownSchemaPillar = cerrors.Register("unknown-schema-pillar", cerrors.Meta{
	ShortCode: "E0267",
	Message:   "a schema block declares a key that names no data pillar",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ValidationDetail describes a validation failure: which condition it
// violates, where it was found, what was required there, what was found
// instead, and what to do about it.
//
// Err is required, and it is the field the rendered message is built around:
// the sentence its condition is registered with is what the message opens
// with, so a failure carrying no condition would have no sentence to report.
// NewValidationError refuses one. Every other field is optional and is omitted
// from the message when empty; which entity is at fault comes from the kind and
// name given to NewValidationError rather than from anything here.
type ValidationDetail struct {
	// FieldPath locates the failure inside the document, e.g. ".values.port".
	// A check that has only the file it read passes that instead — the same
	// dual meaning cerrors.SourceLocation.File carries, which is where this
	// value lands.
	FieldPath string
	// Rule identifies the violated rule the way the entity's reference
	// documentation names it. It is set by NewShapeRuleError from the
	// identifier a ShapeRuleViolation carries and is empty for every other
	// failure; see ShapeRuleViolation.Rule for what the identifiers are.
	Rule string
	// Constraint states what this occurrence required, e.g. "must be >= 1024".
	// It is for what the condition's own registered sentence cannot say —
	// never a restatement of it, which would be a second copy of the rule's
	// wording sitting next to the first.
	Constraint string
	// Actual is the offending value, e.g. "80".
	Actual string
	// Suggestion is the advice for satisfying the rule, e.g. "Use a port
	// number >= 1024".
	Suggestion string
	// Err is the identity of the condition the failure names, matched via
	// errors.Is. Required.
	Err error
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
// It panics when kind names no authoring domain — the zero kind included — and
// when detail names no registered condition, so that every failure this
// constructor produces has a sentence to open its message with.
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
	// The check is for a registered SENTENCE rather than merely a non-nil error,
	// because a sentence is what the rendered message opens with: an error
	// carrying no registered identity would render as a value list with nothing
	// saying which rule it is about, and would be unmatchable by any consumer
	// besides. cerrors.MessageOf walks the chain, so an identity wrapped on its
	// way here still satisfies it.
	if _, ok := cerrors.MessageOf(detail.Err); !ok {
		panic(fmt.Sprintf("validator: a validation failure against %s %q carries no condition, so the "+
			"rendered message has no registered sentence to open with; set ValidationDetail.Err to the "+
			"identity of the condition the check enforces", kind, name))
	}
	return &ValidationError{kind: kind, name: name, detail: detail}
}

// ShapeRuleViolation describes a catalog entity that breaks one of the shape
// rules governing its kind: which entity, where it was loaded from, which rule,
// and what to do about it.
//
// It is a description handed to NewShapeRuleError, not a failure in its own
// right. It carries no Error method, nothing returns it, and no caller matches
// on it — the failure a rule check returns is always *ValidationError, which is
// the one shape a singular validation failure takes.
type ShapeRuleViolation struct {
	// Kind is the entity kind whose rules were checked. It selects the
	// authoring domain the failure is reported under and supplies the leading
	// word of the entity reference the message renders.
	Kind envelope.Kind
	// Name is the entity's metadata name, e.g. "helm/mariadb".
	Name string
	// Version is the entity's metadata version. Optional: omitted from the
	// rendered reference when empty.
	Version string
	// Path is the file the entity was loaded from. It is the failure's
	// locator, so it is what the rendered diagnostic's source pointer reads.
	Path string
	// Rule identifies the violated rule the way the entity's reference
	// documentation names it — a kebab-case word or short phrase naming what
	// the rule constrains, never a position in a list. Optional: omitted from
	// the message when empty.
	Rule string
	// Suggestion is the rule-specific advice for satisfying it.
	Suggestion string
	// Err is the sentinel callers match on.
	Err error
}

// NewShapeRuleError builds the validation failure a ShapeRuleViolation
// describes.
//
// What the rule REQUIRES is not supplied here, and there is no field for it.
// That is the sentence the rule's condition is registered with, and it is what
// the rendered message opens with — so a rule's wording lives with the rule's
// identity and a second copy cannot drift from it. A violation that also
// carried the requirement as text would be that second copy, agreeing on the
// day it was written and diverging the day either side is reworded.
//
// The rule IDENTIFIER is a different thing and does travel here. It is not
// derivable from the sentence, and it is what lets a reader find the rule in
// the entity's reference documentation, so it renders as one of the values the
// occurrence supplies rather than being folded into the wording.
//
// It panics when Kind names no authoring domain, and when Err carries no
// condition, for the reasons NewValidationError does: both reach here as
// constants chosen where the rule is checked, never from an authored document.
func NewShapeRuleError(v ShapeRuleViolation) *ValidationError {
	name := v.Name
	if v.Version != "" {
		name += ":" + v.Version
	}
	return NewValidationError(v.Kind, name, ValidationDetail{
		FieldPath:  v.Path,
		Rule:       v.Rule,
		Suggestion: v.Suggestion,
		Err:        v.Err,
	})
}

// entity renders the entity reference the failure is reported against, in
// the "<Kind> <name>" shape — e.g. "Component core/api-server:1.0.0".
func (e *ValidationError) entity() string {
	return string(e.kind) + " " + e.name
}

// message renders the one-line description shared by Error and the compiler
// error ToCompilerError produces. Both surfaces report the same failure, so
// there is one rendering rather than two that agree until one is edited.
//
// It OPENS with the sentence the failure's condition is registered with, read
// through cerrors.MessageOf, and appends what this occurrence supplied after
// it. The order is the mechanism rather than a preference. The registered
// sentence is one static string per condition, so it cannot name the entity,
// the key or the value at fault; those values in turn say nothing on their own
// about which rule they violate. Leading with the sentence therefore keeps a
// condition's wording in the one place the condition is declared, and what
// follows it is exactly what that sentence could not carry.
//
// The occurrence's values render as a parenthesised list in a fixed order —
// entity reference, rule identifier, position, constraint, offending value —
// each omitted when this failure supplied none, so a sparse failure reads as a
// sentence rather than as one trailing empty separators. The suggestion follows
// the list as a sentence of its own, because it is advice to act on rather than
// a value to read.
//
// A failure carrying no condition renders the value list alone. That shape is
// unreachable through NewValidationError, which refuses a detail with no
// condition; it exists because an empty composite literal of this type is legal
// from any package, and rendering the values is better than panicking inside an
// error's own Error method.
//
// The composition itself is cerrors.ComposeMessage and is shared with every
// other composer that renders a condition, so what varies here is which values
// this failure contributes and in what order — never the shape they are
// rendered in.
func (e *ValidationError) message() string {
	values := make([]string, 0, 5)
	values = append(values, e.entity())
	if e.detail.Rule != "" {
		values = append(values, "rule "+e.detail.Rule)
	}
	if e.detail.FieldPath != "" {
		values = append(values, "at "+e.detail.FieldPath)
	}
	if e.detail.Constraint != "" {
		values = append(values, e.detail.Constraint)
	}
	if e.detail.Actual != "" {
		values = append(values, "got "+e.detail.Actual)
	}
	return cerrors.ComposeMessage(e.detail.Err, values, e.detail.Suggestion)
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
// The Category is the authoring domain the failure's entity kind belongs to,
// read from the kind the failure carries through the single home of that
// correspondence — see [cerrors.ClassifyEntityKind], which states which kind
// belongs to which domain. It is not restated here: a second copy of the
// correspondence agrees on the day it is written and is free to disagree the
// day either side moves. There is no default to fall through to — a kind
// belonging to no domain is refused rather than substituted for.
//
// The Source location maps the JSON-path FieldPath into the CompilerError's
// SourceLocation.File. FieldPath is a JSON-path-style locator (e.g.,
// ".values.port"), not a real file path; the dual semantics is the same
// as ResolverError.Field. The Message is exactly what Error() renders — one
// composition feeding both surfaces, so the two cannot diverge and a chain walk
// that suppresses a restatement is not defeated by two strings that say the
// same thing differently.
//
// Inner error chain preserved via cerrors.Wrap so errors.Is against the
// validator's sentinel errors continues to match.
func (e *ValidationError) ToCompilerError() *cerrors.CompilerError {
	source := cerrors.SourceLocation{File: e.detail.FieldPath}
	return cerrors.Wrap(e, categoryFor(e.kind), source, e.message())
}
