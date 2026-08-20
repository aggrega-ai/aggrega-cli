// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package errors

import (
	stderrors "errors"
	"fmt"
	"strings"
	"sync"
)

// sealed is the identity of a halting condition. It is declared by registering
// a code and a message together, and it is reached through an exported accessor
// standing beside the registration:
//
//	var errBudgetExceeded = Register("example/budget-exceeded", Meta{
//		Message: "the declared budget was exceeded",
//	})
//
//	// ErrBudgetExceeded fires when …
//	func ErrBudgetExceeded() error { return errBudgetExceeded }
//
// The identity above is illustrative and names no package in this module,
// deliberately. An example naming a real declaration is a second copy of that
// declaration's value, and the two drift the moment either is edited.
//
// # Why the identity is a value no caller can construct
//
// Both the type and its fields are unexported, so the only way to obtain an
// identity is to call the accessor of the package that declares it. That is the
// whole point of the shape: an identity a caller could build for itself would
// let any package claim any condition without importing the package that owns
// it, and the owning package could then never move the condition without
// silently breaking a matcher it cannot see.
//
// The code carried here is an OUTPUT and never an input to matching. A caller
// reads it off an error with CodeOf; writing the same string back produces a
// string, not an identity, and there is no path from one to the other.
//
// # An identity carries nothing that differs between occurrences
//
// One identity value is shared by every occurrence of the condition it names,
// so it cannot express which file or which position produced a particular
// failure. That data belongs on the error instance — see CompilerError and its
// Source — and never on the identity. The distinction is the whole reason both
// objects exist: the identity answers "which rule halted", the instance answers
// "where".
type sealed struct {
	code    string
	message string
}

// Error returns the message the identity was registered with.
//
// The message travels on the identity rather than being looked up, so a caller
// wrapping it with fmt.Errorf("...: %w", err) renders that text, unchanged, at
// every enclosing frame. It can never be empty: an empty message renders as a
// dangling separator inside an enclosing wrap, which reads as a truncated
// diagnostic rather than as the defect it is, and registration refuses one.
func (s *sealed) Error() string { return s.message }

// ErrorCode returns the code the identity was registered under.
//
// The code is the durable identifier of the condition itself. It is
// deliberately separate from the Go accessor that yields the identity, so the
// two carry their stability independently: the accessor can be renamed or moved
// without disturbing the code a condition was registered under.
//
// It is reached from Go through CodeOf or the CodedError interface, and from
// outside Go on the structured diagnostic the compiler-as-service daemon
// publishes, where it travels as a field of its own. That is what the separate
// identifier buys a consumer that does not link this package: it reads the code
// rather than recognising a condition by the wording of its message. What
// `aggrega` prints on a terminal is unchanged — the category prefix, the source
// location and the message text.
func (s *sealed) ErrorCode() string { return s.code }

// CodedError is an error that reports the code of the halting condition it
// names. Every registered identity satisfies it.
//
// It is the interface to match with errors.As when the code is what is wanted
// rather than the identity — see CodeOf, which is the usual way to ask.
type CodedError interface {
	error
	// ErrorCode returns the code of the condition the error names.
	ErrorCode() string
}

// CodeOf returns the code reported by the first error in err's chain that
// reports one, and whether any error in the chain did.
//
// It walks the chain, so the code survives arbitrary wrapping: a caller holding
// an error many frames above the site that raised it still reads the code of
// the condition underneath. An error chain carrying no registered identity
// reports no code, which is a fact about the chain rather than a failure.
func CodeOf(err error) (string, bool) {
	var coded CodedError
	if stderrors.As(err, &coded) {
		return coded.ErrorCode(), true
	}
	return "", false
}

// MessageOf returns the sentence registered for the first condition in err's
// chain that carries one, and whether any error in the chain did.
//
// It is CodeOf's companion and walks the same chain for the same reason: the
// sentence survives arbitrary wrapping, so a surface composing a diagnostic
// many frames above the site that raised the failure still opens with the
// wording the condition was registered under.
//
// It asks a different question from err.Error(), and the difference is the
// whole reason it exists. err.Error() reports the OUTERMOST frame's text,
// which is whatever that frame chose to say; this reports the wording of the
// condition underneath it. A composer reading err.Error() and calling the
// result the condition's wording would be rendering its own wrap, and the two
// agreeing on the day it is written is exactly what would hide the divergence
// the day either is edited.
//
// An error chain carrying no registered identity reports no sentence, which is
// a fact about the chain rather than a failure.
func MessageOf(err error) (string, bool) {
	var coded CodedError
	if stderrors.As(err, &coded) {
		return coded.Error(), true
	}
	return "", false
}

// ComposeMessage renders a diagnostic in the one composition every condition's
// message follows: the condition's registered sentence first, the occurrence's
// own values after it in parentheses, and the advice for this occurrence last.
//
// It is the single implementation of that composition, and it is a function
// rather than a convention for a concrete reason: several composers each
// building the same shape from their own struct fields would be several copies
// of one condition's words, and such copies agree until one of them is edited.
// A composer that needs the shape calls this; a composer whose
// report groups many findings calls it for the header and appends its findings
// beneath, so the leading sentence and the parenthesised occurrence still have
// one definition.
//
// Every part is optional except the shape. A condition carrying no registered
// sentence contributes none, which is a fact about the chain rather than a
// failure — see MessageOf. An occurrence supplying no values gets no
// parentheses, so a sparse failure reads as a sentence rather than as a
// sentence trailing empty separators. Advice belongs to the raise path rather
// than to the identity, which is why it is an argument here and not a field on
// Meta: a sentence registered with advice in it is advice that has to be true
// at every site that raises the condition, and the site where it is wrong is
// the site a reader is most likely to reach.
//
// The sentence is read off the condition rather than passed as text, so a
// caller cannot supply a second copy of the wording by accident.
func ComposeMessage(condition error, values []string, advice string) string {
	var b strings.Builder
	if sentence, ok := MessageOf(condition); ok {
		b.WriteString(sentence)
	}
	if len(values) > 0 {
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		b.WriteString("(")
		b.WriteString(strings.Join(values, ", "))
		b.WriteString(")")
	}
	if advice != "" {
		if b.Len() > 0 {
			b.WriteString(". ")
		}
		b.WriteString(advice)
	}
	return b.String()
}

// Meta is the metadata declared beside an identity at its registration.
//
// It carries the message and nothing else, deliberately. The two other
// properties a halting condition has are derived rather than declared, because
// neither is a property of the identity:
//
//   - Which authoring artifact is at fault is derived from the entity kind
//     involved in a particular failure — see ClassifyEntityKind. Declaring it
//     per identity would be a second home for a value that already has one.
//   - Which stage of the pipeline raised the condition is a property of the
//     occurrence, not of the identity: a single rule can be raised from more
//     than one stage, so any single declared value would be false for those.
//
// A field is added here only when every identity has a source for it and
// something consumes it. A field populated for a minority is worse than an
// absent one, because it reads as a measured absence rather than an undecided
// question.
type Meta struct {
	// Message is the text the identity's Error method returns: one sentence
	// stating the condition, in the words a reader of a diagnostic sees.
	//
	// It is the single home of that wording, and the composition rule is what
	// makes that true rather than aspirational: a diagnostic naming the
	// condition OPENS with this sentence and appends whatever the occurrence
	// supplies after it — the document, the authored key, the offending value,
	// the advice. The split is forced by what each side can carry. This
	// sentence is one static string per condition, so it cannot name the
	// occurrence; the occurrence's values mean nothing without the sentence
	// that says which rule they are about. Composing the wording a second time
	// from the occurrence's own fields would leave two copies of one
	// condition's words, and the copy is what drifts — an agreeing copy most
	// of all, because the agreement is what hides the divergence until one
	// side is edited.
	//
	// Read the sentence off an error with MessageOf, which walks the chain the
	// way CodeOf does.
	//
	// It must not be empty: an empty sentence renders as a dangling separator
	// inside an enclosing wrap, and registration refuses one.
	Message string
}

var (
	registryMu sync.RWMutex
	registry   = map[string]*sealed{}
)

// Register declares the halting condition named by code, with the metadata in
// meta, and returns its identity. The call stands as a package-level
// declaration, with the accessor that publishes it directly beside:
//
//	var errBudgetExceeded = Register("example/budget-exceeded", Meta{
//		Message: "the declared budget was exceeded",
//	})
//
//	// ErrBudgetExceeded fires when …
//	func ErrBudgetExceeded() error { return errBudgetExceeded }
//
// The identity above is illustrative and names no package in this module,
// deliberately. An example naming a real declaration is a second copy of that
// declaration's value, and the two drift the moment either is edited.
//
// The returned type is unexported, and that is the design rather than an
// oversight. A caller binds the result with type inference and never names it,
// which is precisely what leaves the identity unconstructible anywhere but
// here. Publishing it through a function rather than an exported variable is
// the other half: a variable an importer can see is a variable an importer can
// reassign, and a reassigned identity silently stops matching everywhere.
//
// A code value is chosen so that distinct conditions stay distinct. Two
// conditions sharing one code become two identities whose codes collide on the
// wire, and the second registration is refused outright. Codes are therefore
// qualified by the declaring package as well as the rule, which keeps two
// same-named rules in different packages apart, and are kebab-case in keeping
// with every other identifier the platform defines.
//
// Register panics, rather than reporting an error, on each of three conditions.
// All three are defects in how the program is assembled rather than anything a
// user can cause, and registration runs at package initialisation, so a panic
// fires on every run and every test before any input is read — it cannot reach
// a release. An error return would be the weaker choice here for a mechanical
// reason: the call stands as a declaration whose result is bound to the
// identity, so a returned error has nowhere to go.
//
//   - An empty code. The zero value is not an identity, and admitting it would
//     leave a condition with nothing to report on the wire.
//   - An empty message. The identity would then render as the empty string,
//     which is the silent shape a diagnostic can least afford.
//   - A code already registered, whether or not the two messages agree. Two
//     registrations for one code means two identities a consumer outside Go
//     cannot tell apart; permitting the agreeing case would leave a hole that
//     opens silently the day one of the messages is edited.
func Register(code string, meta Meta) *sealed {
	registryMu.Lock()
	defer registryMu.Unlock()

	if code == "" {
		panic("errors: Register called with an empty code")
	}
	if meta.Message == "" {
		panic(fmt.Sprintf("errors: Register called with an empty message for code %q", code))
	}
	if _, dup := registry[code]; dup {
		panic(fmt.Sprintf("errors: Register called twice for code %q", code))
	}

	id := &sealed{code: code, message: meta.Message}
	registry[code] = id

	return id
}
