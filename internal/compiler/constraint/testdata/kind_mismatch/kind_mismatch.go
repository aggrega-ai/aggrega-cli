// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package kindmismatch must NOT compile. It declares a check over the node kind
// and then hands it to the entry that presents a document, which is the mistake
// a check author actually makes: the predicate reads facts that are complete
// early, so presenting it a document looks harmless.
//
// It fails because each subject kind carries its own unexported marker, so one
// kind's subject is not the other's however similar the two look. The framework
// invokes a check once per instance of its declared subject, and that is what the
// refusal protects: a node check handed a document would run once for the whole
// file instead of once per node, silently.
package kindmismatch

import (
	"errors"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// ErrRuleBroken stands in for a real sentinel.
var ErrRuleBroken = errors.New("rule broken")

type view struct{ names []string }

// NodeRule declares the node kind: once per component instance.
var NodeRule = constraint.Check[view, constraint.NoHostPowers, constraint.NodeSubject]{
	Sentinel: ErrRuleBroken,
	Severity: constraint.SeverityFatal,
	Run: func(v view, s constraint.NodeSubject) []constraint.Objection {
		var out []constraint.Objection
		for _, n := range v.names {
			out = append(out, s.Raise(ErrRuleBroken, n))
		}
		return out
	},
}

// Run hands the node-kind check to the document entry.
func Run(sourceFile string, names []string) []constraint.Objection {
	return constraint.RunOnDocument(sourceFile, view{names: names}, NodeRule)
}
