// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// advisoriesFromObjections renders what a check raised into the finding shape
// this package's callers already read.
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
// wrapped error, so a caller matching the finding by identity keeps matching.
// The entity names the Aggregate the objections are about.
//
// Findings come back in the order the objections were raised, which is the order
// the check was invoked in.
//
// Returns nil when nothing was raised.
func advisoriesFromObjections(
	objections []constraint.Objection,
	aggregateName string,
) []*ResolverError {
	if len(objections) == 0 {
		return nil
	}
	entity := fmt.Sprintf("Aggregate %s", aggregateName)
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
