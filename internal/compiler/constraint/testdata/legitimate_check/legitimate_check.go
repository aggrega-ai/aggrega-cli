// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package legitimatecheck is the positive control for the subject-kind gate: the
// program that MUST compile. It declares a check as a value over the document
// kind and hands it to the document entry, which is the shape a check is written
// in.
//
// It differs from the program under testdata/kind_mismatch in exactly one thing
// — the subject kind the check declares — so a failure there is attributable to
// the kinds refusing to substitute rather than to anything else.
package legitimatecheck

import (
	"errors"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// ErrUnreadParameter stands in for a real sentinel.
var ErrUnreadParameter = errors.New("parameter is never read")

type view struct{ names []string }

// UnreadParameter is the check as a value: the rule's sentinel, what a violation
// does to the build, and the predicate over the facts and one subject.
var UnreadParameter = constraint.Check[view, constraint.NoHostPowers, constraint.DocumentSubject]{
	Sentinel: ErrUnreadParameter,
	Severity: constraint.SeverityWarning,
	Run: func(v view, s constraint.DocumentSubject) []constraint.Objection {
		var out []constraint.Objection
		for _, n := range v.names {
			out = append(out, s.Raise(ErrUnreadParameter, n))
		}
		return out
	},
}

// Run presents one document to the check and returns what it raised.
func Run(sourceFile string, names []string) []constraint.Objection {
	return constraint.RunOnDocument(sourceFile, view{names: names}, UnreadParameter.Run)
}
