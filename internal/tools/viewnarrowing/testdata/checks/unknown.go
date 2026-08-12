// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package checks

import (
	"errors"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

var (
	// ErrAssertionNameCollision stands in for a real sentinel.
	ErrAssertionNameCollision = errors.New("two assertions share a name")

	// ErrFileCollision stands in for a real sentinel.
	ErrFileCollision = errors.New("two artifacts emit the same path")
)

// localView is a view declared here rather than taken from the chain. It is the
// shape a check takes while the framework has no invocation site supplying a
// view, and it is exactly the shape nothing can be placed from: the declaration
// names no stage, so there is no narrowest sufficient answer to compute.
type localView struct{ names []string }

func (l localView) EntityAssertionNameSet(string) []string { return l.names }

// LocallyDeclaredViewRule declares that local view.
var LocallyDeclaredViewRule = constraint.Check[localView, constraint.DocumentAssertionSubject]{
	Sentinel: ErrAssertionNameCollision,
	Severity: constraint.SeverityFatal,
	Run: func(v localView, s constraint.DocumentAssertionSubject) []constraint.Objection {
		seen := map[string]bool{}
		var out []constraint.Objection
		for _, name := range v.EntityAssertionNameSet(s.SourceFile()) {
			if seen[name] {
				out = append(out, s.Raise(ErrAssertionNameCollision, name))
			}
			seen[name] = true
		}
		return out
	},
}

// MisspelledFactRule reads an accessor no view on the chain declares. The reading
// cannot place a fact the chain does not carry, so it reports the fact rather
// than a stage — and reporting it is what keeps a misspelling from being read as
// a check needing nothing.
var MisspelledFactRule = constraint.Check[constraint.RenderedView, constraint.BoundaryProviderPathSubject]{
	Sentinel: ErrFileCollision,
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.RenderedView, s constraint.BoundaryProviderPathSubject) []constraint.Objection {
		seen := map[string]bool{}
		var out []constraint.Objection
		for _, path := range v.EmittedFilePathSets(s.Boundary(), s.Provider()) {
			if seen[path] {
				out = append(out, s.Raise(ErrFileCollision, path))
			}
			seen[path] = true
		}
		return out
	},
}
