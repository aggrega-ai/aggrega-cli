// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package checks

import (
	"errors"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

var (
	// ErrLocalsOrder stands in for a real sentinel.
	ErrLocalsOrder = errors.New("a local reads a name declared after it")

	// ErrEdgeSocketUndeclared stands in for a real sentinel.
	ErrEdgeSocketUndeclared = errors.New("an edge names a socket the component type does not declare")

	// ErrProvidesUnresolved stands in for a real sentinel.
	ErrProvidesUnresolved = errors.New("a provides key resolved to nothing")
)

// ViewHandedToAHelperRule hands its view to a function, so which facts it reads
// is settled outside its own body. It must be reported as unreadable rather than
// cleared: a verdict drawn from the accessors visible here would be a verdict
// over a requirement set known to be incomplete.
var ViewHandedToAHelperRule = constraint.Check[constraint.HydratedView, constraint.NodeLocalsKeySubject]{
	Sentinel: ErrLocalsOrder,
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.HydratedView, s constraint.NodeLocalsKeySubject) []constraint.Objection {
		if orderedCorrectly(v, s.NodeID()) {
			return nil
		}
		return []constraint.Objection{s.Raise(ErrLocalsOrder, s.LocalsKey())}
	},
}

func orderedCorrectly(v constraint.HydratedView, node string) bool {
	return len(v.DeclaredLocalsKeys(node)) > 0
}

// SubjectOnlyRule reads nothing from its view. Its declaration therefore places
// it at nothing — any view satisfies an empty requirement — which is a different
// answer from over-declaration and is reported as its own.
var SubjectOnlyRule = constraint.Check[constraint.GraphView, constraint.EdgeSubject]{
	Sentinel: ErrEdgeSocketUndeclared,
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.GraphView, s constraint.EdgeSubject) []constraint.Objection {
		if s.SocketName() != "" {
			return nil
		}
		return []constraint.Objection{s.Raise(ErrEdgeSocketUndeclared, s.SocketName())}
	},
}

// PredicateElsewhereRule supplies its predicate as a named function rather than
// writing it in place, so its body is not here to read.
var PredicateElsewhereRule = constraint.Check[constraint.HydratedView, constraint.NodeProvidesKeySubject]{
	Sentinel: ErrProvidesUnresolved,
	Severity: constraint.SeverityWarning,
	Run:      runProvidesResolved,
}

func runProvidesResolved(v constraint.HydratedView, s constraint.NodeProvidesKeySubject) []constraint.Objection {
	if _, resolved := v.ResolvedNodeProvides(s.NodeID(), s.ProvidesKey()); resolved {
		return nil
	}
	return []constraint.Objection{s.Raise(ErrProvidesUnresolved, s.ProvidesKey())}
}
