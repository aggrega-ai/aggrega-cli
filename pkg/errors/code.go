// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package errors

import (
	stderrors "errors"
	"fmt"
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
// The code is the stable identifier a consumer outside Go reads — a wire
// payload, a log line, a diagnostic table. It is deliberately separate from the
// Go accessor that yields the identity, because the two have different
// audiences: the Go name can be refactored without disturbing the code, and the
// code can be read by a consumer that cannot import Go at all.
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
	// Message is the text the identity's Error method returns. It is the
	// user-visible description of the condition and must not be empty.
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
