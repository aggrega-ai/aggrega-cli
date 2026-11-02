// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package documentview

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcekind"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// FeatureRecipe presents one authored FeatureRecipe to a check as
// named facts: whether it declares a source pointer or a values overlay, the kind
// its structured source names, the reference its source names, and the id each
// of its pipeline steps declares.
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
// document-kind axis exists to answer.
//
// It satisfies the whole document-stage view, which declares facts a FeatureRecipe
// cannot carry: an Aggregate's text and parameter contract, an entity's
// inheritance chain, a Manifest's annotation bindings, a Recipe's own blocks. The
// embedded base supplies those and refuses each at the point a check reads it,
// naming the document kind that does carry it. What holds this type to the facts a
// FeatureRecipe itself carries is the obligation asserted at the bottom of this
// file: a FeatureRecipe fact added to the registry stops this type compiling, and a
// fact carried by any other kind does not reach it.
type FeatureRecipe struct {
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

	// sourceKind is the kind the document's structured source names, and
	// sourceKindWritten whether it names one.
	sourceKind        string
	sourceKindWritten bool

	// sourceReference is the reference the document's source names, as authored,
	// and sourceReferenceInURLKey whether it sits under a structured source's
	// `url:` key rather than being the source itself.
	sourceReference         string
	sourceReferenceInURLKey bool

	// pipelineStepIDs are the ids the pipeline's steps declare, one per step, in
	// the order the steps are written.
	pipelineStepIDs []string
}

// NewFeatureRecipe presents the FeatureRecipe a load is holding: the
// file it was read from, and the answers produced out of that document.
//
// The answers are produced here rather than taken from the caller because each
// has exactly one producer in this package, and routing the presentation through
// those producers is what keeps each fact single-valued. A caller passing its own
// answers could pass ones taken from a different document, or derived differently.
func NewFeatureRecipe(path string, fr *types.FeatureRecipe) FeatureRecipe {
	kind, written := authoredSourceKind(fr.Spec.Source)
	reference, inURLKey := authoredSourceReference(fr.Spec.Source)
	return FeatureRecipe{
		path:                    path,
		source:                  !sourceIsEmpty(fr.Spec.Source),
		valuesMapping:           !fr.Spec.ValuesMapping.IsEmpty(),
		sourceKind:              kind,
		sourceKindWritten:       written,
		sourceReference:         reference,
		sourceReferenceInURLKey: inURLKey,
		pipelineStepIDs:         authoredPipelineStepIDs(fr.Spec.Pipeline),
	}
}

// FeatureRecipePipelineStepIds returns the ids the presented FeatureRecipe's
// pipeline steps declare, one per step, in the order the steps are written.
func (v FeatureRecipe) FeatureRecipePipelineStepIds(document string) []string {
	v.refuseOtherDocument(document, "FeatureRecipePipelineStepIds")
	return v.pipelineStepIDs
}

// FeatureRecipeSourceDeclared reports whether the presented FeatureRecipe
// declares a source pointer.
func (v FeatureRecipe) FeatureRecipeSourceDeclared(document string) bool {
	v.refuseOtherDocument(document, "FeatureRecipeSourceDeclared")
	return v.source
}

// FeatureRecipeValuesMappingDeclared reports whether the presented FeatureRecipe
// declares a values overlay.
func (v FeatureRecipe) FeatureRecipeValuesMappingDeclared(document string) bool {
	v.refuseOtherDocument(document, "FeatureRecipeValuesMappingDeclared")
	return v.valuesMapping
}

// FeatureRecipeAuthoredSourceKind is the kind the presented FeatureRecipe's
// structured source names, as authored, and whether it names one.
func (v FeatureRecipe) FeatureRecipeAuthoredSourceKind(document string) (string, bool) {
	v.refuseOtherDocument(document, "FeatureRecipeAuthoredSourceKind")
	return v.sourceKind, v.sourceKindWritten
}

// FeatureRecipeAuthoredSourceReference is the reference the presented
// FeatureRecipe's source names, as authored, and whether it sits under the
// structured form's `url:` key.
func (v FeatureRecipe) FeatureRecipeAuthoredSourceReference(document string) (string, bool) {
	v.refuseOtherDocument(document, "FeatureRecipeAuthoredSourceReference")
	return v.sourceReference, v.sourceReferenceInURLKey
}

// FeatureRecipeSourceKindServed reports whether this program serves kind, read
// from the served set's one declaration.
func (v FeatureRecipe) FeatureRecipeSourceKindServed(kind string) bool {
	return sourcekind.Served(kind)
}

// refuseOtherDocument refuses an accessor call naming a document this value was
// not built for, because answering would hand back this document's fact under
// another document's name.
func (v FeatureRecipe) refuseOtherDocument(document, accessor string) {
	if document == v.path {
		return
	}
	panic(fmt.Sprintf(
		"FeatureRecipe.%s asked about document %q while presenting %q: "+
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
	_ constraint.DocumentView               = FeatureRecipe{}
	_ constraint.FeatureRecipeDocumentFacts = FeatureRecipe{}
)
