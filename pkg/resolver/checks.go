// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// This file holds this package's rules expressed as constraint declarations:
// each is a value naming the sentinel it enforces, what a violation does to the
// build, the facts it reads, and what it is invoked once per. Declared that way,
// a rule's point in the compilation follows from the facts it reads rather than
// from where a call happens to sit, and the provenance its diagnostic carries is
// supplied to it rather than assembled in its body.
//
// A declaration here is unexported, unlike the ones the compiler's internal
// packages carry. This package is published, so an exported declaration would put
// a value typed on an internal framework type into this package's API, where a
// consumer outside this module could not name that type. Nothing about placing a
// declaration depends on its name being exported.

// parametersReferenceRoot is the template path an Aggregate's declared parameters
// are read through, and therefore the root a reference census over the Aggregate's
// own text is taken under.
const parametersReferenceRoot = ".parameters"

// declaredParameterRead reports a parameter the Aggregate's contract declares
// that no reference in the Aggregate's own text reads.
//
// A declared parameter is the authoring surface for varying one value per
// deployment: a Release fulfils the declaration, and a `{{ .parameters.<key> }}`
// reference carries the fulfilled value into the values it belongs to. A
// declaration no reference reads leaves that variation with nowhere to land, so
// whatever a Release supplies for it, the rendered output is the same.
//
// It advises and never halts. A parameter may be declared ahead of the reference
// that will read it, and a reading of the text alone cannot tell that from a
// mistake.
//
// It reads one fact, the document's authored text. The declared names are
// deliberately not read here: the rule is evaluated once per declared name, so the
// subject already carries the one name this evaluation is about, and reading the
// whole set would be asking for something already in hand.
//
// Text that does not parse as a template yields no reference set, and nothing is
// reported for it. Reporting every declaration as unread on a parse failure would
// bury the diagnostic raised over the same unparseable text.
var declaredParameterRead = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentParameterSubject,
]{
	Sentinel: ErrUnreadParameter(),
	Severity: constraint.SeverityWarning,
	Run: func(
		v constraint.DocumentView,
		s constraint.DocumentParameterSubject,
	) []constraint.Objection {
		references, err := gotmpl.CollectTemplateFieldAccesses(
			string(v.RawAggregateText(s.SourceFile())))
		if err != nil {
			return nil
		}
		// The one-key question goes through the same reference census the whole-set
		// reading uses, handed only the name this evaluation is about. What counts
		// as a read is derived from the references and the root alone, so a name's
		// verdict is the same asked by itself as asked among the others — which is
		// what lets the two share one definition of a read instead of agreeing by
		// eye.
		asked := types.OrderedMap[any]{}
		asked.Set(s.ParameterName(), nil)
		if len(gotmpl.UnreadKeys(references, parametersReferenceRoot, asked)) == 0 {
			return nil
		}
		return []constraint.Objection{s.Raise(ErrUnreadParameter(), s.ParameterName())}
	},
}
