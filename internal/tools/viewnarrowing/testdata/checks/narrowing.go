// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package checks is the corpus the narrowing reading is proven against: checks
// written in the shape a real check is written in, each shaped to exercise one
// answer.
//
// It is a fixture rather than a compiled package, and one of its members is
// deliberately shaped so that it would not type-check.
package checks

import (
	"errors"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

var (
	// ErrSocketRequirement stands in for a real sentinel.
	ErrSocketRequirement = errors.New("a required socket is not populated")

	// ErrSlotRequirement stands in for a real sentinel.
	ErrSlotRequirement = errors.New("a required slot is not filled")

	// ErrParameterUnresolved stands in for a real sentinel.
	ErrParameterUnresolved = errors.New("a declared parameter resolved to nothing")
)

// OverDeclaringSocketRule declares the graph view and reads only facts that are
// complete two stages earlier, so it is placed later in the compilation than the
// facts it reads require. That is the whole defect being read for.
var OverDeclaringSocketRule = constraint.Check[constraint.GraphView, constraint.NoHostPowers, constraint.NodeSocketSubject]{
	Sentinel: ErrSocketRequirement,
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.GraphView, s constraint.NodeSocketSubject) []constraint.Objection {
		componentType, declared := v.InstanceComponentType(s.NodeID())
		if !declared {
			return nil
		}
		if !v.SocketRequired(componentType, s.SocketName()) {
			return nil
		}
		return []constraint.Objection{s.Raise(ErrSocketRequirement, s.SocketName())}
	},
}

// ExactlyDeclaredParameterRule is the control, and the reason it is here is that
// without it a reading which flagged every check would satisfy the
// over-declaration case while proving nothing. It declares the topology view and
// reads a topology-stage fact, so the narrowest view that suffices is the one it
// declares and nothing is reported about it.
var ExactlyDeclaredParameterRule = constraint.Check[constraint.TopologyView, constraint.NoHostPowers, constraint.NodeParameterSubject]{
	Sentinel: ErrParameterUnresolved,
	Severity: constraint.SeverityWarning,
	Run: func(v constraint.TopologyView, s constraint.NodeParameterSubject) []constraint.Objection {
		var out []constraint.Objection
		for _, name := range v.DeclaredParameterKeys(s.SourceFile()) {
			if _, resolved := v.ResolvedParameter(s.NodeID(), name); !resolved {
				out = append(out, s.Raise(ErrParameterUnresolved, name))
			}
		}
		return out
	},
}

// AccessorThroughAVariableRule reads one of its two facts through a variable
// rather than calling it in place. It over-declares exactly as the first check
// does, and it is separate because taking an accessor as a value is the shape a
// reading over call expressions alone would miss — it would clear this check and
// report the first one, from bodies that read the same fact.
//
// Reading one fact in place and the other through a variable is deliberate, and
// the variable-held one is what the fixture is for: a reading that saw only call
// expressions would find the component type here and miss the slot predicate,
// reporting a narrower requirement set than the body actually has. That is a
// sharper failure than seeing nothing, because it looks like an answer.
var AccessorThroughAVariableRule = constraint.Check[constraint.GraphView, constraint.NoHostPowers, constraint.NodeSlotSubject]{
	Sentinel: ErrSlotRequirement,
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.GraphView, s constraint.NodeSlotSubject) []constraint.Objection {
		componentType, declared := v.InstanceComponentType(s.NodeID())
		if !declared {
			return nil
		}
		required := v.SlotRequired
		if !required(componentType, s.SlotName()) {
			return nil
		}
		return []constraint.Objection{s.Raise(ErrSlotRequirement, s.SlotName())}
	},
}
