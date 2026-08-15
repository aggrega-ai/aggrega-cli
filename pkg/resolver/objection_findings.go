// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// findingsFromObjections renders what a check raised into the finding shape this
// package's callers already read.
//
// A check hands back the sentinel naming the rule and the provenance of what it
// objected about, and nothing else: it is deliberately not a diagnostic, so the
// shape a finding takes is decided at the boundary it crosses rather than inside
// the rule. This is that decision for the boundary a resolve reports across, and
// it is the only place the conversion is made, so every rule reporting this way
// reports identically.
//
// Every field is derived and none is re-typed. The message is the sentinel's own
// registered text, so a rule's wording lives with the rule's identity and a
// second copy cannot drift from it. The locator becomes the field the finding
// positions at, which is what lets a front end place it on the offending
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
func findingsFromObjections(
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
			Message: o.Sentinel().Error(),
			Err:     o.Sentinel(),
		})
	}
	return out
}
