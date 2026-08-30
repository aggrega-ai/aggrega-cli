// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// The reading over the routing slots a Manifest's annotation bindings claim,
// expressed as a constraint declaration. See checks.go for what a declaration
// here is.
//
// It sits in its own file rather than beside the reserved-prefix reading for the
// reason that one states: two rules sharing a stage and a subject are still two
// rules, and one file holding both would suggest a reader looking for either
// would find both.

// annotationBindingRouteIsClaimedOnce reports an annotation binding claiming a
// routing slot an earlier binding already claimed on the same Feature.
//
// An annotation binding routes one annotation's value into named fields of a
// Feature attachment, and the slot it claims is a pillar and a dotted path taken
// together. Two bindings claiming one slot on one Feature leave expansion with no
// answer to "which binding routes this path", so the build stops rather than
// picking one.
//
// It halts, and it could not be advisory. The ambiguity is in the authored
// document rather than in what the compiler makes of it, so admitting both
// bindings and reporting the fact afterwards would leave a rendered output whose
// content depended on which of two equally authorised bindings was read last.
//
// It reports against the LATER binding and names the earlier one, because the
// earlier claim is the one already in effect and the later is what an author
// edits to resolve the ambiguity. Where several earlier bindings claim the slot,
// the first claimant is named: it is the one whose claim the rest collided with.
//
// It reads three facts. The Feature the binding routes to is read for both of the
// things it answers — the second result establishes that the presented Manifest
// declares a binding of this key at all, and a key it does not declare is passed
// over — and the Feature itself is what scopes the comparison, since one path
// claimed on two different Features is two slots. The routes are what the
// comparison is over. The binding key set supplies the bindings declared before
// this one, and it is read rather than accumulated because a rule invoked once
// per binding holds no state between invocations: what it may look back at is the
// document, which the view presents whole.
//
// It compares against earlier bindings only, never against the subject's own
// routes. A binding cannot claim one slot twice: each pillar's path list is
// declared unique by the Manifest's own schema, and the two pillars are different
// slots by construction, so the document envelope refuses that shape strictly
// earlier than this rule is invoked — and it refuses it with a line and column,
// which this rule could not carry.
//
// Its detail leads with the offending binding's own key, which is what the
// registered wording is about and so carries no caption. It has to be there
// rather than left to the locator: an annotation key carries a period by
// convention, and the locator dialect stops at a segment containing one, so the
// position reaches the registry's block and not the key inside it. The earlier
// claimant, the Feature both target, the slot they meet on and what to edit
// follow, each captioned, because the registered wording names none of them.
var annotationBindingRouteIsClaimedOnce = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentAnnotationBindingSubject,
]{
	Sentinel: ErrManifestCrossBindingPillarCollision(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.DocumentAnnotationBindingSubject,
	) []constraint.Objection {
		document, key := s.SourceFile(), s.AnnotationBindingKey()
		feature, declared := v.ManifestAnnotationBindingFeature(document, key)
		if !declared {
			return nil
		}
		routes := v.ManifestAnnotationBindingRoutes(document, key)
		if len(routes) == 0 {
			return nil
		}

		// The bindings declared before this one, on the same Feature. The walk
		// stops at the subject's own key rather than filtering it out, because
		// "earlier" is what makes one of two colliding bindings the one already in
		// effect, and the registry's order is the author's.
		var earlier []string
		for _, other := range v.ManifestAnnotationBindingKeys(document) {
			if other == key {
				break
			}
			if otherFeature, ok := v.ManifestAnnotationBindingFeature(document, other); ok &&
				otherFeature == feature {
				earlier = append(earlier, other)
			}
		}

		var out []constraint.Objection
		for _, route := range routes {
			for _, other := range earlier {
				if !claimsRoute(v.ManifestAnnotationBindingRoutes(document, other), route) {
					continue
				}
				out = append(out, s.RaiseWith(ErrManifestCrossBindingPillarCollision(),
					constraint.Uncaptioned(key),
					constraint.Captioned("already claimed by", other),
					constraint.Captioned("on Feature", feature),
					constraint.Captioned("at pillar", route.Pillar),
					constraint.Captioned("and path", route.Path),
					constraint.Captioned("give one of the two a different entry under", "maps"),
				))
				break
			}
		}
		return out
	},
}

// claimsRoute reports whether a binding's routes carry the given slot. A slot is
// the pillar and the path together, so the comparison is over the pair: one path
// routed to two pillars is two slots, and a Feature declaring one property name
// under both pillars is a shape the schema admits.
func claimsRoute(routes []constraint.AnnotationBindingRoute, want constraint.AnnotationBindingRoute) bool {
	for _, route := range routes {
		if route == want {
			return true
		}
	}
	return false
}
