// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// FindingsFromObjections renders what a check raised into the finding shape this
// package's callers already read.
//
// A check hands back the sentinel naming the rule and the provenance of what it
// objected about, and nothing else: it is deliberately not a diagnostic, so the
// shape a finding takes is decided at the boundary it crosses rather than inside
// the rule. This is that decision for the boundary a resolve reports across.
//
// Every field is derived and none is re-typed. The sentinel becomes the wrapped
// error, so a caller matching the finding by identity keeps matching — both the
// caller that drops one rule's findings out of a halt set and the caller that
// matches a rule through the error a failed resolve returns. The locator becomes
// the field the finding positions at, which is what lets a front end place it on
// the offending authoring key rather than at the head of the file.
//
// 🔴 The message is built from the rule's detail VALUES and never from
// [constraint.ObjectionMessage]. The finding renders through the one composition
// every diagnostic naming a condition follows, which reads the rule's registered
// wording off the sentinel itself; [constraint.ObjectionMessage] already opens
// with that same wording, so handing its result over as a value would state the
// rule's sentence twice in one line. Taking the values instead is not a second
// wording — it is the same rule reporting through a boundary that supplies
// subject of its own, the document a finding is attributed to and the locator it
// sits at, which the rule cannot know. The values arrive already spelled as a
// message reads them, from [constraint.Objection.RenderedDetail], the same
// spelling [constraint.ObjectionMessage] renders from: one wording, one detail
// spelling, two shapes.
//
// They are joined with the separator the composition itself puts between values,
// so a rule's values read as further entries in the finding's value list rather
// than as one opaque entry inside it.
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
// invoked from the package that holds that stage's product, and every such check
// reporting into this finding shape reaches it here rather than re-deriving it.
// A stage whose callers read a different diagnostic shape converts for itself and
// shares the wording through the sentinel, so the two shapes stay two shapes and
// the wording stays one.
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
			Message: strings.Join(o.RenderedDetail(), ", "),
			Err:     o.Sentinel(),
		})
	}
	return out
}
