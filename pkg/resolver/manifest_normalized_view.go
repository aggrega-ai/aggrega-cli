// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// manifestNormalizedView presents a Manifest's annotation binding registry and
// the catalog beside it, together with the facts of a normalized compilation.
//
// It satisfies [constraint.NormalizedView], and the compiler holds it to that
// contract at the bottom of this file rather than a comment claiming it.
//
// # Why this presentation exists at all
//
// A rule over an authored annotation's value reads three things complete at
// different points: the routing the Manifest's binding declares, the contract of
// the Feature that routing targets, and the value the author wrote in one of the
// authoring documents. The presentation the resolve builds for the last of those
// holds the Aggregate and the Release and answers no question about a Manifest —
// it refuses them, deliberately, because an empty answer there would be
// indistinguishable from a Manifest whose registry declares nothing. So such a
// rule needs a presentation of its own, exactly as the document stage needs one
// presentation per authored document.
//
// A rule that reads only the first two needs none of this: those two are the
// catalog stage's, and [manifestCatalogView] is the narrower value that holds
// them. This type exists for the rules that also read a compilation.
//
// # Nothing here is re-projected
//
// Every fact this value does not itself hold is answered by the presentation the
// resolve already built, embedded and delegated to. One fact has one producer,
// and two producers of a fact are free to disagree about it with nothing
// comparing them.
//
// The Manifest's own facts and the catalog stage's are delegated too, to the
// narrower value that already states them, rather than projected a second time
// from the same registry and the same catalog here. That keeps one reading of
// each: a second projection agreeing today is what hides the divergence the day
// either moves.
type manifestNormalizedView struct {
	constraint.NormalizedView

	// bindings is the catalog-stage presentation of the Manifest this value was
	// built for. The Manifest's own facts and the resolved contract of every
	// Feature its bindings target are answered by delegating to it.
	bindings *manifestCatalogView
}

// newManifestNormalizedView presents the Manifest a caller is holding together
// with the facts the resolve's own presentation states.
//
// The later presentation is taken rather than reached for, because a view
// projects facts already produced. The Manifest beside the catalog is built here
// rather than taken for the reason the document presentation is derived rather
// than passed: a caller supplying its own could supply one built from a
// different document.
func newManifestNormalizedView(
	path string,
	manifest *types.Manifest,
	later constraint.NormalizedView,
	catalog CatalogLookup,
) *manifestNormalizedView {
	return &manifestNormalizedView{
		NormalizedView: later,
		bindings:       newManifestCatalogView(path, manifest, catalog),
	}
}

// ManifestAnnotationBindingKeys returns the annotation keys the presented
// Manifest's registry declares, in author order.
func (v *manifestNormalizedView) ManifestAnnotationBindingKeys(document string) []string {
	return v.bindings.ManifestAnnotationBindingKeys(document)
}

// ManifestAnnotationBindingFeature returns the Feature the named binding routes
// to, and whether the presented Manifest declares a binding of that key at all.
func (v *manifestNormalizedView) ManifestAnnotationBindingFeature(
	document string, binding string,
) (string, bool) {
	return v.bindings.ManifestAnnotationBindingFeature(document, binding)
}

// ManifestAnnotationBindingRoutes returns the routing slots the named binding
// claims on the Feature it targets, in author order.
func (v *manifestNormalizedView) ManifestAnnotationBindingRoutes(
	document string, binding string,
) []constraint.AnnotationBindingRoute {
	return v.bindings.ManifestAnnotationBindingRoutes(document, binding)
}

// AnnotationBindingSubkeyTarget returns what one subkey written under an
// annotation key has to satisfy — the Feature, the pillar and the path its value
// is routed to — and whether the presented Manifest routes it anywhere at all.
//
// It delegates like the binding facts above, so the routing lookup has one
// reading rather than a second one here that agrees with it today.
func (v *manifestNormalizedView) AnnotationBindingSubkeyTarget(
	binding string, subkey string,
) (constraint.AnnotationSubkeyTarget, bool) {
	return v.bindings.AnnotationBindingSubkeyTarget(binding, subkey)
}

// CompiledFeatureSchema returns the contract of a Feature a binding targets,
// resolved, and whether there was a contract that resolved at all.
//
// It delegates to the value that performed the resolution, so every rule invoked
// over either presentation reads one result. The presentation embedded here
// answers the same fact by resolving per call and keeping nothing, which is why
// the delegation is written rather than left to the embedding: a rule that needs
// the three resolution outcomes told apart has to reach the value that kept them.
func (v *manifestNormalizedView) CompiledFeatureSchema(featureType string) (map[string]any, bool) {
	return v.bindings.CompiledFeatureSchema(featureType)
}

// The compiler holds this type to the contract rather than a comment claiming it:
// a signature drifting from the declared facts stops compiling here.
var _ constraint.NormalizedView = (*manifestNormalizedView)(nil)
