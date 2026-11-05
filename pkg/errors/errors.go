// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package errors

import (
	stderrors "errors"
	"fmt"
	"strings"
)

// Category names which concern a given CompilerError belongs to. Most of the
// values name the authoring artifact at fault, and the set as a whole partitions
// the compiler's error surface by who must act to resolve the failure: developer
// (Aggregate), operator (Release / Manifest), catalog author (Recipe / Component
// / Feature / FeatureRecipe / Plugin), provider engine (translation cannot
// express the requested output), the remote registry / network layer (a fetch of
// a pinned artifact failed at lock or build time), an operator-attached policy
// plugin (a governance verdict rejected the build — the fix is to satisfy the
// enforced policy or reconsider its attachment, not to edit an authoring file),
// or the trust and signing layer (a signature, a signing policy, or a recorded
// trusted identity refused the operation).
//
// The values are deliberately not counted here. The block below is the
// statement of how many there are, and a number written beside it reads as
// current while lagging the set it counts.
//
// The zero value is the absence of a category, and it is a meaningful state
// rather than an unset field: not every failure the compiler reports has a
// concern the reader can be routed to. A failure whose cause lies in the
// compiler itself is the case that arises today. The rendered head omits an
// absent category the same way it omits an absent short code, so nothing
// stands in for it.
//
// Being no fault of an authored artifact is NOT on its own what leaves the
// category absent, and reading it that way would be the wrong test. Several of
// the values above name a concern rather than an authoring surface and are
// printed for exactly such failures — an acquisition fault, a governance
// verdict, a trust refusal. What leaves the category absent is having no
// concern to route the reader to at all.
type Category string

// The error categories.
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
	// authoring fault — it is a policy the build must satisfy. The
	// remediation is to bring the input into compliance with the enforced
	// policy or to reconsider the policy attachment, not to correct an
	// authoring error in the Aggregate.
	PolicyError Category = "POLICY_ERROR"
	// TrustError surfaces a failure in the trust and signing layer: a
	// signature that does not verify, an artifact that carries none where one
	// is required, a signer whose identity is not among those trusted, a trust
	// policy or trust root that cannot be read, or a plugin whose recorded
	// trusted identity no longer matches what resolves.
	//
	// It names a CONCERN rather than an authoring surface, which is what lets
	// one value cover two remediations — "the trust configuration you wrote is
	// wrong" and "this artifact failed verification". That is deliberate: the
	// category answers which concern to look at, and the condition's short code
	// beside it in the head is what distinguishes the specific failure. Two
	// categories would put the distinction in the token that is a routing hint
	// and leave the token that is an identity saying it twice.
	//
	// Distinct from FetchError, whose own documentation scopes it to fetching,
	// transport and digest verification: a valid signature from an untrusted
	// identity is none of those, and its remediation is a trust decision rather
	// than an operational one. Distinct from PolicyError, which is a governance
	// attachment's verdict on an assembled build rather than a judgement about
	// what signed an artifact.
	TrustError Category = "TRUST_ERROR"
)

// SourceLocation points at the most specific authoring locator available for
// the failure, and at the instance the failure concerns where there is one. The
// two are separate coordinates because they answer separate questions: which
// document to open, and which instance of the build the failure is about.
//
// File carries the authoring locator. It is a real filesystem path where the
// failure has a position in an authored document — the typical shape for a
// catalog content error surfacing a YAML position. Where no document applies it
// is another locator the reader can still act on: an entity name, a Release
// name, or a JSON-path field locator such as ".values.port"; a caller that has
// only a field locator should pass it rather than leave the pointer empty. It
// never carries an instance: an instance in File would read as a document the
// reader cannot open.
//
// Instance names the instance of the build the failure concerns — a component
// the Aggregate declares, a component a module contributes, a component the
// build derives, the module itself — by the identity the build gives it, which
// for a module's component is the dotted path through the module and for a
// derived component the identity the build composes for it. It is set beside a File where the reporting
// site holds the document as well, and alone where it holds none.
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
	File     string
	Locator  string
	Line     int
	Column   int
	Instance string
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
// A non-empty Instance follows whatever the position and locator produced, as
// "(instance <id>)", and stands alone as "instance <id>" where they produced
// nothing. It is introduced by its own word in both shapes so that an instance
// is never read as a file name, which is the confusion keeping it out of File
// exists to prevent.
//
// This is the one place a position is composed, so every surface that names one
// names it in the same shape. An empty Locator and an empty Instance return
// exactly what the file, line and column alone produce, which is what keeps a
// caller that supplies neither rendering byte-identically.
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
	case position == "":
		position = s.Locator
	default:
		position = position + " at " + s.Locator
	}
	switch {
	case s.Instance == "":
		return position
	case position == "":
		return "instance " + s.Instance
	default:
		return position + " (instance " + s.Instance + ")"
	}
}

// CompilerError is the compiler's categorised-error type. A CompilerError
// surfaces at CLI output through the build command's error path; the CLI
// formatter renders it in the documented shape (category tag in brackets,
// source pointer, message, optional template excerpt, optional pillar
// snapshot).
//
// # Every field is private, and that is the enforcement rather than a style
//
// A value of this type is reachable only through the constructors below, and
// the constructors are the whole of the choice a caller makes about identity:
// [Raise] names a registered condition, [Unidentified] states that there is
// none, and [Wrap] re-categorises a chain that carries whatever identity it
// carries. Exported fields would leave a fourth way in — a composite literal, or
// a write onto an envelope somebody else built — and that way bypasses the
// choice entirely, producing something that renders exactly like a registered
// diagnostic while naming no condition at all.
//
// The bypass is closed by the language rather than by agreement: a composite
// literal naming any of these fields from another package does not compile, and
// neither does a write to one. Unexporting SOME of them would not do it —
// Go admits a composite literal that sets only the exported fields of a type
// that also has unexported ones — so the seal holds only while every field is
// private. Do not export one to save an accessor.
//
// The reading side is unchanged: the accessors below return what the fields
// hold, and Unwrap returns the wrapped chain so errors.Is and errors.As traverse
// it transparently.
type CompilerError struct {
	category   Category
	source     SourceLocation
	message    string
	expression string
	context    map[string]any
	wrap       error
}

// Category returns the concern the failure was filed under, or the empty
// Category when it has none. An absent category is a state rather than an unset
// field — see the Category type — so a caller renders its absence rather than
// standing something in for it.
func (e *CompilerError) Category() Category {
	if e == nil {
		return ""
	}
	return e.category
}

// Source returns the authoring locator the failure points at. The zero
// SourceLocation means the failure is not attributable to a position.
func (e *CompilerError) Source() SourceLocation {
	if e == nil {
		return SourceLocation{}
	}
	return e.source
}

// Message returns the human-facing sentence the occurrence carries.
func (e *CompilerError) Message() string {
	if e == nil {
		return ""
	}
	return e.message
}

// Expression returns the failing template expression the occurrence carries, or
// the empty string when it carries none.
func (e *CompilerError) Expression() string {
	if e == nil {
		return ""
	}
	return e.expression
}

// Context returns the per-pass pillar snapshot taken at the failure point, or
// nil when none was taken.
//
// The map is returned as it is held rather than copied, so a caller that writes
// to it writes to the error's own snapshot. Nothing in the compiler does, and a
// copy on every read would cost every rendering to guard against a caller that
// has no reason to.
func (e *CompilerError) Context() map[string]any {
	if e == nil {
		return nil
	}
	return e.context
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
//
// An absent Category is omitted along with its brackets, the same way the
// terminal formatter omits it. Empty brackets would assert that a category was
// expected and is missing, which is a different claim from a failure that has
// no authoring surface to name — and this string is re-wrapped into enclosing
// frames, so the wrong claim would be carried up rather than left where it was
// made.
func (e *CompilerError) Error() string {
	if e == nil {
		return "<nil>"
	}
	var head string
	if e.category != "" {
		head = fmt.Sprintf("[%s] ", e.category)
	}
	src := e.source.String()
	if src == "" {
		return head + e.message
	}
	return fmt.Sprintf("%s%s: %s", head, src, e.message)
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

// HeadedCauseChain renders err as CauseChain does, with the short code of the
// condition err names written into the head, as the terminal's diagnostic carries
// it: the bracket of the first envelope in the chain — the frame whose category
// heads the terminal's rendering — becomes `[<category> <code>]`. A chain naming
// no registered condition renders exactly as CauseChain renders it.
//
// It is the one-line form for a surface that reports a stopped run as a sentence
// rather than as the terminal's multi-line diagnostic — a warning line, a report
// line — so a reader meeting the failure there can look its condition up as they
// can from a build's own halt. The code is read off the whole chain and qualified
// by QualifiedShortCodeOf, the composition the terminal's head reads, so the two
// surfaces print one token for one condition.
//
// The first envelope's bracket is the first occurrence of that bracket in the
// flattened text, because a frame renders its own text ahead of the frames it
// wraps. Where that envelope carries no category there is no bracket to write
// into, and the code opens the line as a bracket of its own, which is how the
// terminal heads a failure that crossed no categorising boundary.
func HeadedCauseChain(err error) string {
	text := CauseChain(err)
	code := QualifiedShortCodeOf(err)
	if code == "" {
		return text
	}
	var head *CompilerError
	if stderrors.As(err, &head) && head != nil && head.category != "" {
		bracket := "[" + string(head.category) + "]"
		if i := strings.Index(text, bracket); i >= 0 {
			return text[:i] + "[" + string(head.category) + " " + code + "]" + text[i+len(bracket):]
		}
	}
	return "[" + code + "] " + text
}

// Unwrap returns the wrapped inner error so errors.Is and errors.As
// traverse the chain. Returns nil when no inner error is carried.
func (e *CompilerError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.wrap
}

// WithExpression returns the envelope carrying expression as the failing
// template expression a renderer reports, and returns the same envelope so the
// call reads as part of the construction it decorates.
//
// It is a decoration rather than a constructor, and the distinction is what
// keeps it outside the seal: it cannot bring an envelope into existence and so
// cannot produce one that skipped the identity choice [Raise] and
// [Unidentified] force. What it can do is fill the one field no constructor
// takes, which is why it exists at all — the expression belongs to a template
// failure and to nothing else, so a parameter for it on every constructor would
// be a parameter almost every caller passes empty.
func (e *CompilerError) WithExpression(expression string) *CompilerError {
	if e == nil {
		return nil
	}
	e.expression = expression
	return e
}

// Raise builds the occurrence of a registered halting condition: the identity
// says WHICH rule halted, and the arguments beside it say where and about what.
//
// The condition is required and it is typed, which is the point of this
// constructor existing separately from [Unidentified]. A caller cannot reach
// this path with an error it invented, and the refusal arrives in two halves
// because one route clears the type and the other does not.
//
// The type is the compile-time half. An error written out by hand does not
// satisfy [Identity] — the interface carries a method no importing package can
// declare — so the program handing one to this constructor does not build.
//
// Embedding is the run-time half. A struct that embeds [Identity] has that
// interface's whole method set promoted onto it, the undeclarable method
// included, so it satisfies the parameter's type while naming no registered
// condition; promotion is how Go composes interfaces and a declaration cannot
// decline it. This constructor therefore asserts that the value it was handed
// is one a [Register] call produced, and refuses anything else.
//
// The condition becomes the envelope's wrapped error, so [CodeOf], [ShortCodeOf]
// and [MessageOf] reach it from any frame above, and the rendered head carries
// the condition's short code beside the category.
//
// Pass an empty SourceLocation where the failure is not attributable to a
// position, and nil for context where no pillar snapshot was taken.
//
// A caller holding BOTH a condition and a separate cause composes the chain
// itself — fmt.Errorf("%w: …", condition, …) — and passes the result to [Wrap].
// This constructor deliberately takes no cause parameter: there is one
// established way to join a condition to a cause, and a second one here would
// be a second answer to a question that already has one.
//
// Both wrong values panic — a nil condition, and a condition no [Register]
// call produced. Each is a defect in how the program is assembled rather than
// anything a user can cause, which is the same standing [Register]'s own
// refusals have. The difference worth knowing is when they fire: registration
// runs at package initialisation, so its refusals land before any input is
// read, while these land when the raise path executes. Panicking is still the
// right refusal, because the alternative is a diagnostic that names a condition
// nobody registered — sending a reader to look up a code that resolves to
// nothing, or to whichever condition happens to hold the number.
func Raise(condition Identity, category Category, source SourceLocation, message string, context map[string]any) *CompilerError {
	return &CompilerError{
		category: category,
		source:   source,
		message:  message,
		context:  context,
		wrap:     registeredCondition("Raise", condition),
	}
}

// registeredCondition returns the registration behind condition, and panics when
// there is none: a nil condition, or one no [Register] call produced. constructor
// names the constructor that was handed it, so the refusal says which call to
// correct.
//
// It is the one place a constructor's run-time half of the seal is written, so
// every constructor demanding a registered condition refuses the same values for
// the same reasons. See [Raise] for why the refusal is a panic.
func registeredCondition(constructor string, condition Identity) *sealed {
	if condition == nil {
		panic("errors: " + constructor + " called with a nil condition; pass the identity the condition's Register call returned, or call Unidentified if the failure names no registered condition")
	}
	// The assertion is against the concrete type rather than against any
	// property of the value, because only this package can produce that type.
	// A value embedding Identity is non-nil at every level and answers every
	// exported read, so nothing readable off it distinguishes it from a
	// registered condition.
	registered, ok := condition.(*sealed)
	if !ok || registered == nil {
		panic("errors: " + constructor + " called with a condition no Register call produced; a type that embeds Identity satisfies the interface without registering anything, so pass the value the condition's Register call returned, or call Unidentified if the failure names no registered condition")
	}
	return registered
}

// Unidentified builds an occurrence that names no registered halting condition.
//
// IT IS AN ESCAPE HATCH, AND THE NAME IS THE POINT. A diagnostic built here
// renders with the category in brackets and no short code beside it, so it
// looks to a reader almost exactly like one that names a condition while
// identifying nothing they can quote, search or look up. The name is what makes
// the gap visible at the call site and countable across the tree, and it is why
// this is a separate function rather than a nil argument to [Raise]: a nil
// passed to a shared constructor states the same thing and states it where
// nobody reads it.
//
// Three classes reach it legitimately, and they are the reason it is not simply
// removed:
//
//   - A refusal that is not a compiler halt at all — a command refusing its own
//     arguments, a file it cannot read, a cancellation. There is no rule for a
//     reader to look up because no rule fired.
//   - A fault in the compiler itself, which has no authoring surface to route
//     the reader to and so carries no category either.
//   - A failure whose condition has not been registered yet. This is the class
//     that shrinks: each such site becomes a [Raise] the day its condition is
//     declared.
//
// Do not "simplify" this back into one constructor with an optional
// identity. The optional-identity shape is exactly what this pair replaces: one
// parameter slot meaning "the condition" at some sites and "a cause" at others
// and "nothing" at most, which no signature can tell apart and no reader can
// either.
func Unidentified(category Category, source SourceLocation, message string, context map[string]any, cause error) *CompilerError {
	return &CompilerError{
		category: category,
		source:   source,
		message:  message,
		context:  context,
		wrap:     cause,
	}
}

// Wrap re-categorises an existing error as a CompilerError with the given
// Category, preserving the inner error chain. Useful at category-boundary
// crossings where an error from one layer (e.g., a parse error originating
// in a Recipe) must surface to the operator under the Catalog category
// rather than its origin layer's category.
//
// The identity of the result is whatever the chain beneath it carries: a chain
// that reaches a registered condition renders the short code in the head, and
// one that does not renders the category alone. That is a property of the
// VALUE passed in, which no signature can state — which is why this is the one
// constructor whose identity is not decided at the call site, and why a caller
// building a diagnostic from scratch reaches for [Raise] or [Unidentified]
// instead of wrapping something merely to get here.
func Wrap(err error, category Category, source SourceLocation, message string) *CompilerError {
	return &CompilerError{
		category: category,
		source:   source,
		message:  message,
		wrap:     err,
	}
}
