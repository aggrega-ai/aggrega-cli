// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package selector

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// MatchApplicability evaluates both applicability surfaces a Feature
// declares against a label set and reports whether that pair admits the
// set. It is the one home for the composition, so every surface asking
// whether a Feature's declared applicability admits a label set — the
// rule that decides an attachment during a build, and a read-only query
// reporting which Features a node's labels match — reaches the same
// answer for the same input by construction rather than by agreeing
// today.
//
// Both surfaces hold, and the two hold differently. The string form's
// requirements hold together, so every requirement inside it must be
// satisfied; the list form holds when any single item does. Neither
// surface is branched on presence, because an omitted surface is
// vacuously satisfied either way: an empty selector string declares no
// requirement, and an empty item list declares no alternative. Composing
// them unconditionally is what keeps a Feature that declares only one
// surface from being treated as declaring both.
//
// A declaration that cannot be parsed does not admit the label set. The
// error reports the first unevaluable surface, in argument order, and the
// boolean is false alongside it. That direction is deliberate rather than
// defensive: treating a malformed selector as admitting everything would
// let a typo silently widen what a Feature applies to, which is the one
// failure mode a label-matching surface must not have.
//
// Both arguments are read and neither is interpreted. A label is opaque
// here — the function evaluates the requirements a declaration states
// against the pairs a label set carries, and infers nothing from a key,
// a value, or a name.
func MatchApplicability(appliesTo string, appliesToAnyOf []types.SelectorTerm, labels types.OrderedMap[string]) (bool, error) {
	// Both surfaces are evaluated before either error is consulted, so
	// the surface reported as unevaluable is fixed by argument order and
	// not by which of the two happens to be evaluated first.
	allHold, allErr := MatchString(appliesTo, labels)
	anyHolds, anyErr := MatchAnyOf(appliesToAnyOf, labels)
	if allErr != nil {
		return false, allErr
	}
	if anyErr != nil {
		return false, anyErr
	}
	return allHold && anyHolds, nil
}
