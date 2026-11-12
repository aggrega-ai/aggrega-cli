// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// The reading over which pillar a Manifest's annotation binding routes each of
// its paths through, expressed as a constraint declaration. See checks.go for
// what a declaration here is.
//
// It sits in its own file rather than beside the slot-claim reading for the
// reason that one states: two rules sharing a stage and a subject are still two
// rules, and one file holding both would suggest a reader looking for either
// would find both.

// annotationBindingPathRoutesThroughOnePillar reports an annotation binding
// declaring one path under both of its pillar lists.
//
// An annotation binding routes one annotation's value into named fields of a
// Feature attachment, and each path it declares sits under the pillar that
// carries it. A path declared under both is incoherent rather than ambiguous:
// an annotation subkey fires one pillar's contribution per authoring side, so
// there is no reading under which both claims take effect.
//
// It advises and does not halt, and that is the whole difference between this
// reading and the one over routing slots beside it. The routing is decided
// rather than undecided — the lookup the runtime performs answers with the infra
// pillar for a path declared under both, consistently, so the rendered output of
// a document carrying the overlap is as determined as that of one without it.
// What the author loses is the values side of that path, which the Aggregate
// surface can then no longer reach; the build halts there, at expansion, if the
// Aggregate ever tries. Refusing the document here would refuse authoring the
// compiler accepts, for a shape that renders deterministically.
//
// It reports against the losing claim. The walk is over the routes the values
// pillar declares, and each one whose path the infra pillar also claims raises,
// because the values-side claim is the one the tie-break drops and so the one an
// author edits. The reverse walk would report the claim that survives.
//
// Its findings appear in the order the values pillar declares its paths, which
// is the order the author wrote them. Nothing here sorts: the routes the view
// presents are already in a fixed order it declares and gives its reason for, and
// a second ordering imposed on top of it would be a second answer to a question
// already settled.
//
// It reads two facts. The Feature the binding routes to is read for both of the
// things it answers — the second result establishes that the presented Manifest
// declares a binding of this key at all, and a key it does not declare is passed
// over — and the Feature itself is what the finding names, since a path is a
// property of the Feature's schema and means nothing without it. The routes are
// what the comparison is over.
//
// Its detail leads with the offending binding's own key, which is what the
// registered wording is about and so carries no caption. It has to be there
// rather than left to the locator: an annotation key carries a period by
// convention, and the locator dialect stops at a segment containing one, so the
// position reaches the registry's block and not the key inside it. The Feature,
// the path, the pillar the tie-break routes it through and what to write instead
// follow, each captioned, because the registered wording names none of them.
var annotationBindingPathRoutesThroughOnePillar = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentAnnotationBindingSubject,
]{
	Sentinel: ErrAnnotationBindingPillarOverlap(),
	Severity: constraint.SeverityWarning,
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

		var out []constraint.Objection
		for _, route := range routes {
			if route.Pillar != bindingRoutePillarValues {
				continue
			}
			if !claimsRoute(routes, constraint.AnnotationBindingRoute{
				Pillar: bindingRoutePillarInfra,
				Path:   route.Path,
			}) {
				continue
			}
			out = append(out, s.RaiseWith(ErrAnnotationBindingPillarOverlap(),
				constraint.Uncaptioned(key),
				constraint.Captioned("on Feature", feature),
				constraint.Captioned("at path", route.Path),
				constraint.Captioned("which the routing tie-break sends through the pillar",
					bindingRoutePillarInfra),
				constraint.Captioned("declare a second binding under another annotation key to route it through",
					bindingRoutePillarValues),
			))
		}
		return out
	},
}
