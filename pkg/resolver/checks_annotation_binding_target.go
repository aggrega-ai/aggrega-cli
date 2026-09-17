// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// The reading over the Feature an annotation binding routes to, expressed as a
// constraint declaration. See checks.go for what a declaration here is.
//
// It sits in its own file rather than beside the two readings over the same
// coordinate for the reason those two state: rules sharing a stage and a subject
// are still separate rules, and one file holding several would suggest a reader
// looking for any of them would find them all.

// annotationBindingResolvesAgainstItsTargetFeature reports an annotation binding
// whose target Feature the catalog does not hold, and one routing to a pillar
// path that Feature does not declare.
//
// An annotation binding routes one annotation's value into named fields of a
// Feature attachment. Both halves of that have to be real for the routing to mean
// anything: the Feature has to exist, and each field the binding names has to be
// one the Feature declares. Neither is knowable from the Manifest alone, which is
// what places this reading at the stage holding one authored document beside the
// catalog it resolves against, rather than at the document the binding is written
// on. It needs nothing beyond that pair — no Aggregate, no Release, nothing
// normalized — so it reads none of a compilation's facts and declares no view
// that carries them.
//
// It halts. A binding routing to a field nothing declares delivers its value
// nowhere, and admitting the build would produce an attachment silently missing
// the configuration the author wrote the annotation to supply.
//
// It reports against the binding, not against the Feature or the annotation. The
// binding is what the author edits: the Feature is a catalog entity the author of
// a Manifest may not own, and the annotation is written on a third document by a
// third hand. Reporting at either of those sends a reader to a file whose content
// is not the fault.
//
// It reads four facts. The Feature the binding routes to is read for both of the
// things it answers — the second result establishes that the presented Manifest
// declares a binding of this key at all, and a key it does not declare is passed
// over. Whether the catalog holds that Feature is settled next and separately,
// because the resolved contract answers the same absence for a Feature the
// catalog lacks as for one declaring no contract, so a rule that did not settle
// presence first would report the wrong one. The resolved contract is what a
// route is measured against, and the routes are what the measurement is over.
//
// A Feature whose contract did not resolve is passed over here rather than
// reported. The rule reads that outcome and never produces it: the resolution
// keeps the failure and whatever holds it reports it once, so raising here as
// well would report one fault twice and would attribute a catalog entity's own
// defect to the binding that happened to reach it.
//
// The path walk defaults to skip. A schema shape the walk cannot statically
// resolve — a reference, pattern properties, a composition, an open object —
// leaves the route neither confirmed nor refuted, and a halt on an uncertain
// answer would refuse authoring the product accepts. Only a route the walk
// reached an explicit property map for, and did not find named there, halts.
//
// Its detail leads with the binding's own key, which is what the registered
// wording is about and so carries no caption. It has to be there rather than left
// to the locator: an annotation key carries a period by convention, and the
// locator dialect stops at a segment containing one, so the position reaches the
// registry's block and not the key inside it. What the binding routes to, where
// it routes, and what to edit follow, each captioned, because the registered
// wording names none of them.
var annotationBindingResolvesAgainstItsTargetFeature = constraint.Check[
	constraint.CatalogView,
	constraint.DocumentAnnotationBindingSubject,
]{
	Sentinel: ErrAnnotationBindingTargetUnresolved(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.CatalogView,
		s constraint.DocumentAnnotationBindingSubject,
	) []constraint.Objection {
		document, key := s.SourceFile(), s.AnnotationBindingKey()
		featureType, declared := v.ManifestAnnotationBindingFeature(document, key)
		if !declared {
			return nil
		}
		if !v.CatalogFeaturePresent(featureType) {
			return []constraint.Objection{
				s.RaiseWith(ErrAnnotationBindingTargetUnresolved(),
					constraint.Uncaptioned(key),
					constraint.Captioned("routes to Feature", featureType),
					constraint.Captioned(
						"which this build's catalog does not hold; correct or publish the Feature named under",
						"feature"),
				),
			}
		}
		contract, resolved := v.CompiledFeatureSchema(featureType)
		if !resolved {
			return nil
		}

		var out []constraint.Objection
		for _, route := range v.ManifestAnnotationBindingRoutes(document, key) {
			named, uncertain := schemaHasPropertyAtPath(contract, route.Pillar, route.Path)
			if uncertain || named {
				continue
			}
			out = append(out, s.RaiseWith(ErrAnnotationBindingTargetUnresolved(),
				constraint.Uncaptioned(key),
				constraint.Captioned("routes to Feature", featureType),
				constraint.Captioned("at pillar", route.Pillar),
				constraint.Captioned("and path", route.Path),
				constraint.Captioned(
					"which that Feature does not declare a property for under", "schema"),
			))
		}
		return out
	},
}
