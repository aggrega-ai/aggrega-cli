// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// featureRecipeDocumentView presents one authored FeatureRecipe to a check as
// named facts: the capability the document declares, and whether it declares a
// source pointer, a values overlay, a workload body or artifact entries.
//
// It answers about the one document it was built for, which is the framework's
// contract rather than a simplification of it. The subject a check over a
// document is invoked with stands for the one document being presented rather
// than a member of a document set, because entity documents are validated one at
// a time as they are read. So the document argument each accessor takes names the
// document this value was built for, and asking about another is a mistake rather
// than a lookup that misses.
//
// It holds the answers rather than the FeatureRecipe, and that is the shape
// rather than a convenience. A view presents facts already produced; holding the
// document would leave every accessor free to consult a field no fact declares,
// which is exactly the reach a view exists to close. Each answer is produced here
// by the one reader of its own question — each block's own emptiness test, and
// the source reader this package already uses everywhere it asks — so the fact
// stays single-valued rather than gaining a second derivation that agrees today.
//
// The facts it presents are named for the FeatureRecipe rather than shared with
// the Recipe's identically-shaped ones, and the separation is deliberate. The two
// kinds carry the same authored blocks and are constrained by rules that differ:
// a Recipe must declare a rendering surface and a FeatureRecipe may legitimately
// declare none. One fact spanning both kinds would let a rule written for one be
// invoked on the other with nothing objecting, which is the coverage question the
// carrier axis exists to answer.
//
// It satisfies the whole document-stage view, which declares facts a FeatureRecipe
// cannot carry: an Aggregate's text and parameter contract, an entity's
// inheritance chain, a Manifest's annotation bindings, a Recipe's own blocks. The
// embedded base supplies those and refuses each at the point a check reads it,
// naming the document kind that does carry it. What holds this type to the facts a
// FeatureRecipe itself carries is the obligation asserted at the bottom of this
// file: a FeatureRecipe fact added to the registry stops this type compiling, and a
// fact carried by any other kind does not reach it.
type featureRecipeDocumentView struct {
	// FeatureRecipeDocumentBase supplies the document-stage facts a
	// FeatureRecipe does not carry, and declares nothing for the ones it does —
	// so this type writes bodies for exactly the FeatureRecipe's own facts.
	constraint.FeatureRecipeDocumentBase

	// path is the workspace-relative file the document was read from. It is the
	// document coordinate itself, which is why an accessor's argument is checked
	// against it.
	path string

	// source is whether the document declares a source pointer.
	source bool

	// valuesMapping is whether the document declares a values overlay.
	valuesMapping bool

	// workload is whether the document declares a workload body.
	workload bool

	// artifacts is whether the document declares any artifact entry.
	artifacts bool

	// capability is the capability the document declares, as authored.
	capability string
}

// newFeatureRecipeDocumentView presents the FeatureRecipe a load is holding: the
// file it was read from, and the answers produced out of that document.
//
// The answers are produced here rather than taken from the caller because each
// has exactly one producer in this package, and routing the presentation through
// those producers is what keeps each fact single-valued. A caller passing its own
// answers could pass ones taken from a different document, or derived differently.
//
// The capability is carried as authored and is not compared against anything
// here. A view presents what the document says; which capability a rule cares
// about is the rule's own business, and a presentation that answered "is this an
// eval FeatureRecipe" would be presenting one rule's predicate as though it were a
// property of the file.
func newFeatureRecipeDocumentView(path string, fr *types.FeatureRecipe) featureRecipeDocumentView {
	return featureRecipeDocumentView{
		path:          path,
		source:        !sourceIsEmpty(fr.Spec.Source),
		valuesMapping: !fr.Spec.ValuesMapping.IsEmpty(),
		workload:      !fr.Spec.Workload.IsEmpty(),
		artifacts:     fr.Spec.Artifacts.Len() > 0,
		capability:    fr.Spec.Capability,
	}
}

// FeatureRecipeSourceDeclared reports whether the presented FeatureRecipe
// declares a source pointer.
func (v featureRecipeDocumentView) FeatureRecipeSourceDeclared(document string) bool {
	v.refuseOtherDocument(document, "FeatureRecipeSourceDeclared")
	return v.source
}

// FeatureRecipeValuesMappingDeclared reports whether the presented FeatureRecipe
// declares a values overlay.
func (v featureRecipeDocumentView) FeatureRecipeValuesMappingDeclared(document string) bool {
	v.refuseOtherDocument(document, "FeatureRecipeValuesMappingDeclared")
	return v.valuesMapping
}

// FeatureRecipeWorkloadDeclared reports whether the presented FeatureRecipe
// declares a workload body.
func (v featureRecipeDocumentView) FeatureRecipeWorkloadDeclared(document string) bool {
	v.refuseOtherDocument(document, "FeatureRecipeWorkloadDeclared")
	return v.workload
}

// FeatureRecipeArtifactsDeclared reports whether the presented FeatureRecipe
// declares any artifact entry.
func (v featureRecipeDocumentView) FeatureRecipeArtifactsDeclared(document string) bool {
	v.refuseOtherDocument(document, "FeatureRecipeArtifactsDeclared")
	return v.artifacts
}

// FeatureRecipeDeclaredCapability is the capability the presented FeatureRecipe
// declares, as the author wrote it.
func (v featureRecipeDocumentView) FeatureRecipeDeclaredCapability(document string) string {
	v.refuseOtherDocument(document, "FeatureRecipeDeclaredCapability")
	return v.capability
}

// refuseOtherDocument refuses an accessor call naming a document this value was
// not built for, because answering would hand back this document's fact under
// another document's name.
func (v featureRecipeDocumentView) refuseOtherDocument(document, accessor string) {
	if document == v.path {
		return
	}
	panic(fmt.Sprintf(
		"featureRecipeDocumentView.%s asked about document %q while presenting %q: "+
			"a view presents one document, so present the document being asked about",
		accessor, document, v.path))
}

// The compiler holds the type to the contract rather than a comment claiming it:
// a signature drifting from the declared facts stops compiling here.
//
// The second assertion is the half the embedded base cannot supply. Embedding
// makes every document-stage fact compile, so the view assertion alone would
// accept a presentation answering none of them; naming the kind's own obligation
// is what makes a FeatureRecipe fact added to the registry fail here, and on no
// presentation of another kind.
var (
	_ constraint.DocumentView               = featureRecipeDocumentView{}
	_ constraint.FeatureRecipeDocumentFacts = featureRecipeDocumentView{}
)
