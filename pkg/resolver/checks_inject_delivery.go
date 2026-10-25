// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The rules over the shape of what an author wrote under one delivery mechanism,
// expressed as constraint declarations. See checks.go for what a declaration here
// is.
//
// Both are asked once per mechanism of every injection block either authoring
// document writes — a component's own, an attachment's, and the one on each
// connection either draws, in the Release as well as the Aggregate — over one
// enumeration and one payload fact, so the two cannot come to disagree about which
// blocks exist. They are two declarations rather than one because they are two
// rules: each check carries the one condition it reports, and a finding is matched
// by that condition.
//
// The Aggregate and Release schemas admit neither fault, so a document read from a
// file is refused when it loads and neither rule finds anything in it. They refuse
// the same faults in a document built through the Go API, which no schema has
// read, and they are asked while the documents are resolved so a build halts
// before anything is assembled from the block.

// injectDeliveryIsAList reports each delivery mechanism whose value is not a list
// of entries: a mapping, a scalar, any other shape.
//
// Every reader of a mechanism reads the list and nothing else, so a mechanism
// holding anything else would deliver nothing, silently. A mechanism carrying no
// value at all delivers nothing either, and that is what an author leaving it
// empty means, so it is not refused.
//
// The finding is positioned at the mechanism, and its detail is the mechanism's
// name: a locator stops short of a key carrying its separator, so the name is
// what still says which mechanism is meant where the position cannot.
var injectDeliveryIsAList = constraint.Check[
	constraint.NormalizedView,
	constraint.InjectDeliverySubject,
]{
	Sentinel: ErrInjectDeliveryShape(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.NormalizedView,
		s constraint.InjectDeliverySubject,
	) []constraint.Objection {
		payload := v.AuthoredInjectDelivery(s.AuthoringDocumentKind(), s.InjectBlockPath(), s.MechanismName())
		if payload == nil {
			return nil
		}
		if _, isList := payload.([]any); isList {
			return nil
		}
		return []constraint.Objection{s.Raise(ErrInjectDeliveryShape(), s.MechanismName())}
	},
}

// injectDeliveryEntryIsUnambiguous reports each delivery mechanism holding an
// entry that carries both `value:` and `from:`.
//
// It reads the list form alone. A mechanism holding anything else is the rule
// above's to report, and its entries are not entries this one could read.
//
// An entry this rule cannot read as an entry — one lacking `in:`, or not a mapping
// at all — is not its question either, and every reader of a mechanism skips such
// an entry exactly as this one does.
//
// One finding is raised per mechanism, positioned at it, and its detail is the
// mechanism's name followed by the `in:` of each offending entry in the order the
// list holds them: a locator names keys and cannot reach into a list, so the
// entries are named by the key the author wrote for each.
var injectDeliveryEntryIsUnambiguous = constraint.Check[
	constraint.NormalizedView,
	constraint.InjectDeliverySubject,
]{
	Sentinel: ErrInjectEntryAmbiguous(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.NormalizedView,
		s constraint.InjectDeliverySubject,
	) []constraint.Objection {
		entries, isList := v.AuthoredInjectDelivery(
			s.AuthoringDocumentKind(), s.InjectBlockPath(), s.MechanismName()).([]any)
		if !isList {
			return nil
		}
		detail := []string{s.MechanismName()}
		for _, entry := range entries {
			in, _, _, hasValue, hasFrom, ok := types.DecodeVerboseInjectEntry(entry)
			if ok && hasValue && hasFrom {
				detail = append(detail, in)
			}
		}
		if len(detail) == 1 {
			return nil
		}
		return []constraint.Objection{s.Raise(ErrInjectEntryAmbiguous(), detail...)}
	},
}
