// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// FindingsFromObjections renders what a check raised into the finding shape this
// package's callers already read.
//
// A check hands back the sentinel naming the rule and the provenance of what it
// objected about, and nothing else: it is deliberately not a diagnostic, so the
// shape a finding takes is decided at the boundary it crosses rather than inside
// the rule. This is that decision for the boundary a resolve reports across, and
// it is the only place the conversion is made, so every rule reporting this way
// reports identically.
//
// Every field is derived and none is re-typed. The message opens with the
// sentinel's own registered text, so a rule's wording lives with the rule's
// identity and a second copy cannot drift from it, and the values the check
// supplied follow it — because the registered text is one static string per
// rule and cannot carry them, so a message that were only that text would drop
// the very thing an author has to act on. The locator becomes the field the
// finding positions at, which is what lets a front end place it on the offending
// authoring key rather than at the head of the file. The sentinel becomes the
// wrapped error, so a caller matching the finding by identity keeps matching —
// both the caller that drops one rule's findings out of a halt set and the
// caller that matches a rule through the error a failed resolve returns.
//
// The entity names the document the findings are about, and it is supplied
// rather than derived because the rules reporting through here span both
// authoring documents: a rule over what the deployment wrote is not attributable
// to the application, and a conversion that named one document for everything
// would send an operator to a file they did not write in.
//
// Findings come back in the order the objections were raised, which is the order
// the check was invoked in.
//
// Returns nil when nothing was raised.
//
// It is exported because a check placed at a later stage of the compilation is
// invoked from the package that holds that stage's product, and a second
// conversion written there would be free to render the same objection
// differently. One conversion is what makes "every rule reporting this way
// reports identically" a property rather than an intention.
func FindingsFromObjections(
	objections []constraint.Objection,
	entity string,
) []*ResolverError {
	if len(objections) == 0 {
		return nil
	}
	out := make([]*ResolverError, 0, len(objections))
	for _, o := range objections {
		out = append(out, &ResolverError{
			Entity:  entity,
			Field:   o.Locator(),
			Message: objectionMessage(o),
			Err:     o.Sentinel(),
		})
	}
	return out
}

// objectionMessage renders what one check raised as the text a finding carries:
// the sentinel's registered wording, then the values the check supplied.
//
// The values are rendered rather than described, because this reads every rule
// and knows none of them: a check's detail is an ordered list of strings whose
// meaning is the rule's, so any attempt here to caption them would be one
// conversion holding a copy of every rule's grammar. Each is quoted, which keeps
// a value carrying a space or a comma readable as one value and makes a value
// the check supplied empty visible instead of vanishing into the separator.
//
// A check that supplied no detail gets the registered wording alone, so a rule
// whose identity says the whole of it reads as a sentence rather than as one
// with an empty parenthesis after it.
func objectionMessage(o constraint.Objection) string {
	registered := o.Sentinel().Error()
	detail := o.Detail()
	if len(detail) == 0 {
		return registered
	}
	quoted := make([]string, 0, len(detail))
	for _, d := range detail {
		quoted = append(quoted, fmt.Sprintf("%q", d))
	}
	return registered + " (" + strings.Join(quoted, ", ") + ")"
}
