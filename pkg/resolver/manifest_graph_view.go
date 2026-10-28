// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// manifestGraphView presents the Manifest a build was compiled with beside the
// graph that build finalised, as the named facts complete once the graph is final.
//
// It satisfies [constraint.GraphView], and the compiler holds it to that contract
// at the bottom of this file rather than a comment claiming it.
//
// # Why a compilation's presentation needs the Manifest put beside it
//
// The presentation of a compilation refuses every fact of one authored document,
// because it presents the compilation rather than one of the documents it was
// compiled from. A rule over one entry of the Manifest's mapping table needs both
// halves at once: the entry, which is a fact of that one document, and what the
// build holds that the entry could name, which is complete only once the graph is
// final. So this value answers the Manifest's own facts from the Manifest's own
// presentation and every other fact from the compilation's, and neither is
// re-derived here: one fact has one producer, and two producers of a fact are free
// to disagree about it with nothing comparing them.
//
// It answers about the one Manifest it was built for, on the terms the Manifest's
// presentation sets: a Manifest fact asked about another document refuses rather
// than answering. The Manifest is presented under no file, because the Manifest a
// build compiles with is the fold of its extends chain and an entry in it may have
// been written in any file of that chain.
type manifestGraphView struct {
	// GraphView answers every fact the compilation presents, and refuses the facts
	// of an authored document the way the compilation's presentation does.
	constraint.GraphView

	// manifest answers the facts the Manifest itself carries.
	manifest manifestDocumentView
}

// ComposedManifestDocument is the document a Manifest a build compiled with is
// presented under: no file, because that Manifest is the fold of its extends
// chain and the fold does not record which file of the chain wrote an entry.
// A caller invoking a rule over that Manifest's entries names it as the document,
// so the subjects it supplies name the document the presentation answers for.
const ComposedManifestDocument = composedManifestHasNoFile

// PresentManifestBesideGraph presents the Manifest a build was compiled with
// beside the presentation of the graph that build finalised, under
// [ComposedManifestDocument].
//
// It returns nil when the graph's presentation is absent, for the reason that
// presentation's own constructor does: a presentation answering every fact of the
// graph from nothing is worse than an absent one, because a rule reading it cannot
// tell an empty answer from a real one. A nil Manifest is presented as one
// declaring no mapping entry, which is what a build compiled with none holds.
func PresentManifestBesideGraph(manifest *types.Manifest, graph constraint.GraphView) constraint.GraphView {
	if graph == nil {
		return nil
	}
	return manifestGraphView{
		GraphView: graph,
		manifest:  newManifestDocumentView(ComposedManifestDocument, manifest),
	}
}

// ManifestRecipeMappingEntries returns the entries of the presented Manifest's
// recipe mapping table, answered by the Manifest's own presentation.
func (v manifestGraphView) ManifestRecipeMappingEntries(document string) []constraint.ManifestRecipeMapping {
	return v.manifest.ManifestRecipeMappingEntries(document)
}

// ManifestRecipeMappingReference returns the reference one entry of the presented
// Manifest's mapping table names, answered by the Manifest's own presentation.
func (v manifestGraphView) ManifestRecipeMappingReference(document, tier, axis, key string) (string, bool) {
	return v.manifest.ManifestRecipeMappingReference(document, tier, axis, key)
}

// ManifestAnnotationBindingKeys returns the annotation keys the presented
// Manifest's registry declares, answered by the Manifest's own presentation.
func (v manifestGraphView) ManifestAnnotationBindingKeys(document string) []string {
	return v.manifest.ManifestAnnotationBindingKeys(document)
}

// ManifestAnnotationBindingFeature returns the Feature one binding routes to,
// answered by the Manifest's own presentation.
func (v manifestGraphView) ManifestAnnotationBindingFeature(document, binding string) (string, bool) {
	return v.manifest.ManifestAnnotationBindingFeature(document, binding)
}

// ManifestAnnotationBindingRoutes returns the routing slots one binding claims,
// answered by the Manifest's own presentation.
func (v manifestGraphView) ManifestAnnotationBindingRoutes(document, binding string) []constraint.AnnotationBindingRoute {
	return v.manifest.ManifestAnnotationBindingRoutes(document, binding)
}

// AnnotationBindingSubkeyTarget returns what one subkey written under an
// annotation key has to satisfy, answered by the Manifest's own presentation.
func (v manifestGraphView) AnnotationBindingSubkeyTarget(binding, subkey string) (constraint.AnnotationSubkeyTarget, bool) {
	return v.manifest.AnnotationBindingSubkeyTarget(binding, subkey)
}

// AnnotationBindingDeclaredRoutes returns the routing slots the binding of one
// annotation key claims, answered by the Manifest's own presentation.
func (v manifestGraphView) AnnotationBindingDeclaredRoutes(binding string) ([]constraint.AnnotationBindingRoute, bool) {
	return v.manifest.AnnotationBindingDeclaredRoutes(binding)
}

// ManifestExtendsChainCycleEdge is answered by the Manifest's own presentation,
// which refuses it: the fact is read off a walk of the chain, not off the
// document.
func (v manifestGraphView) ManifestExtendsChainCycleEdge(document string) (constraint.ExtendsChainCycleEdge, bool) {
	return v.manifest.ManifestExtendsChainCycleEdge(document)
}

// ManifestExtendsChainLength is answered by the Manifest's own presentation, for
// the reason [manifestGraphView.ManifestExtendsChainCycleEdge] states.
func (v manifestGraphView) ManifestExtendsChainLength(document string) (int, bool) {
	return v.manifest.ManifestExtendsChainLength(document)
}

// The compiler holds this type to the contract rather than a comment claiming it.
// The second assertion is the half embedding cannot supply: the embedded
// compilation presentation makes every Manifest fact compile while refusing each
// one, so naming the Manifest's own obligation is what makes a Manifest fact added
// to the registry fail here until it is answered.
var (
	_ constraint.GraphView             = manifestGraphView{}
	_ constraint.ManifestDocumentFacts = manifestGraphView{}
)
