// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package errors

import (
	"fmt"
	"sync"
)

// Code is the identity of a halting condition, declared as a constant and
// carrying its message in one registration beside it:
//
//	const ErrPathCollision Code = "renderer/path-collision"
//
//	var _ = Register(ErrPathCollision, Meta{
//		Message: "two artifacts render to the same output path",
//	})
//
// Code implements error on a value receiver, so a constant satisfies the error
// interface directly. It is both what a caller raises and what a caller matches
// against, so errors.Is keeps working through arbitrary wrapping:
//
//	errors.Is(err, ErrPathCollision)
//
// Declaring the identity as a constant is what makes a misspelling a
// compile-time failure rather than a comparison that silently never matches.
//
// # A Code carries nothing that differs between occurrences
//
// One Code value is shared by every occurrence of the condition it names, so it
// cannot express which file or which position produced a particular failure.
// That data belongs on the error instance — see CompilerError and its Source —
// and never on the identity. The distinction is the whole reason both objects
// exist: the identity answers "which rule halted", the instance answers "where".
//
// # A Code value is chosen so that distinct conditions stay distinct
//
// The value is the identity, so two conditions sharing one value become one
// condition: errors.Is would begin matching across them and only one of the two
// messages could be registered. Values are therefore qualified by the declaring
// package as well as the rule, which keeps two same-named rules in different
// packages apart. Codes are kebab-case, in keeping with every other identifier
// the platform defines.
type Code string

// Error returns the message registered for c.
//
// The message is registered rather than embedded so that the identity and the
// text a reader sees are declared together in one place. A caller wrapping a
// Code with fmt.Errorf("...: %w", c) therefore renders the registered text,
// unchanged, at every enclosing frame.
//
// A Code with no registration returns text naming the unregistered code. It
// never returns the empty string: an empty message renders as a dangling
// separator inside an enclosing wrap, which reads as a truncated diagnostic
// rather than as the defect it is, and a caller cannot tell it from a condition
// that legitimately had nothing to say.
func (c Code) Error() string {
	registryMu.RLock()
	meta, ok := registry[c]
	registryMu.RUnlock()
	if !ok {
		return fmt.Sprintf("unregistered error code %q", string(c))
	}
	return meta.Message
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
	// Message is the text Code.Error returns. It is the user-visible
	// description of the condition and must not be empty.
	Message string
}

var (
	registryMu sync.RWMutex
	registry   = map[Code]Meta{}
)

// Register records meta as the metadata for code. It returns an empty value so
// that the call can stand as a package-level declaration beside the identity it
// describes:
//
//	const ErrPathCollision Code = "renderer/path-collision"
//
//	var _ = Register(ErrPathCollision, Meta{
//		Message: "two artifacts render to the same output path",
//	})
//
// The return type is deliberately empty rather than the code itself. Returning
// the code would invite binding an identity to the call's result, which makes
// the identity a variable and gives up the compile-time guarantee that a
// misspelled identity fails to build.
//
// Register panics, rather than reporting an error, on each of three conditions.
// All three are defects in how the program is assembled rather than anything a
// user can cause, and registration runs at package initialisation, so a panic
// fires on every run and every test before any input is read — it cannot reach
// a release. An error return would be the weaker choice here for a mechanical
// reason: the call stands as a declaration whose result is discarded, so a
// returned error has nowhere to go.
//
//   - An empty code. The zero value is not an identity, and admitting it would
//     make every uninitialised Code match a registered one under errors.Is.
//   - An empty message. Code.Error would then return the empty string, which is
//     exactly the silent shape the unregistered path is written to avoid.
//   - A code already registered, whether or not the two messages agree. Two
//     registrations for one identity means two declaration sites, which is the
//     condition this type exists to remove; permitting the agreeing case would
//     leave a hole that opens silently the day one of the messages is edited.
func Register(code Code, meta Meta) struct{} {
	registryMu.Lock()
	defer registryMu.Unlock()

	if code == "" {
		panic("errors: Register called with an empty code")
	}
	if meta.Message == "" {
		panic(fmt.Sprintf("errors: Register called with an empty message for code %q", string(code)))
	}
	if _, dup := registry[code]; dup {
		panic(fmt.Sprintf("errors: Register called twice for code %q", string(code)))
	}
	registry[code] = meta

	return struct{}{}
}
