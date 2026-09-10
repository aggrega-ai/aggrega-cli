// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package refcycle is a content-agnostic cycle detector for recursive
// reference expansion. A caller threads an in-flight key stack through its
// recursion and checks each new key before descending; a key already in flight
// is a cycle. The package is reference-kind-neutral — cross-document `$ref`
// (SchemaLibrary) expansion and `$import` (MappingLibrary) expansion both route
// their cycle detection here, so the guard cannot diverge between the two
// surfaces. Callers supply a canonical, spelling-independent key (host-less,
// version-stripped) so a cycle cannot evade detection by inconsistent spelling.
package refcycle

import cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"

// ErrCycle is the shared sentinel for a detected reference-expansion cycle.
// Callers wrap it with surface-specific context (the offending chain, the
// `$ref` / `$import` keyword, source location); consumers match the failure
// with errors.Is(err, ErrCycle()).
func ErrCycle() error { return errCycle }

var errCycle = cerrors.Register("reference-cycle", cerrors.Meta{
	ShortCode: "E0146",
	Message:   "circular reference expansion",
})

// Check reports whether key is already in flight within stack. When it is,
// cyclic is true and chain is the full cycle path — stack followed by the
// repeated key — for the caller's diagnostic message. Otherwise chain is nil
// and cyclic is false. Check does not mutate stack.
func Check(stack []string, key string) (chain []string, cyclic bool) {
	for _, inFlight := range stack {
		if inFlight == key {
			return append(append([]string{}, stack...), key), true
		}
	}
	return nil, false
}

// Push returns a new stack with key appended; the input is not mutated, so a
// caller can thread the extended stack into one recursive branch without
// affecting sibling branches.
func Push(stack []string, key string) []string {
	return append(append([]string{}, stack...), key)
}
