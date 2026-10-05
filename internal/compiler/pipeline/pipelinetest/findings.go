// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipelinetest

import (
	"errors"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// Findings returns every finding a refused build carries for one condition, in
// the order the refusal holds them.
//
// A build refused by several rules at once reports them together, and a test
// asserting on one rule's finding has to read that finding rather than the whole
// refusal: a position or a phrase another rule's finding happens to carry would
// otherwise satisfy, or defeat, an assertion that is about this rule alone. So
// the refusal is walked to the findings it is composed of, and each one that
// carries the condition — matched by identity, never by its text — is returned
// whole, with the category it was filed under and the position it names.
//
// A finding is taken as it is met and not unwrapped further, so a finding that
// names the condition is returned once however its own chain is built.
func Findings(err error, condition error) []*cerrors.CompilerError {
	var out []*cerrors.CompilerError
	var walk func(error)
	walk = func(e error) {
		if e == nil {
			return
		}
		if finding, ok := e.(*cerrors.CompilerError); ok && errors.Is(finding, condition) {
			out = append(out, finding)
			return
		}
		switch wrapped := e.(type) {
		case interface{ Unwrap() []error }:
			for _, inner := range wrapped.Unwrap() {
				walk(inner)
			}
		case interface{ Unwrap() error }:
			walk(wrapped.Unwrap())
		}
	}
	walk(err)
	return out
}
