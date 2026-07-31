// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package legitimate is the positive control for the forgery gate: the one
// program under testdata that MUST compile. It uses the framework the way a
// check is meant to — the subject it is handed raises, and the objection is
// read back through the exported accessors.
//
// It exists so a compile-failure gate cannot pass by failing everything. A
// framework package that did not compile, an import path that had moved, or a
// toolchain invocation that was broken would make every forgery case "fail to
// compile" for a reason that has nothing to do with forgery; this program is
// what tells those apart.
package legitimate

import (
	"errors"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// ErrUnreadParameter stands in for a real sentinel.
var ErrUnreadParameter = errors.New("parameter is never read")

type view struct{ names []string }

// Run presents one document and returns what the check raised, reading every
// exported accessor on the objection so a signature change breaks this program.
func Run(sourceFile string, names []string) []string {
	objections := constraint.RunOnDocument(sourceFile, view{names: names},
		constraint.Check[view, constraint.NoHostPowers, constraint.DocumentSubject]{
			Sentinel: ErrUnreadParameter,
			Severity: constraint.SeverityWarning,
			Run: func(v view, s constraint.DocumentSubject) []constraint.Objection {
				var out []constraint.Objection
				for _, n := range v.names {
					out = append(out, s.Raise(ErrUnreadParameter, n))
				}
				return out
			},
		})

	var rendered []string
	for _, o := range objections {
		line := o.SourceFile() + o.Locator() + ": " + o.Sentinel().Error()
		for _, d := range o.Detail() {
			line += " " + d
		}
		rendered = append(rendered, line)
	}
	return rendered
}
