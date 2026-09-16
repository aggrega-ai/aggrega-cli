// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// recipeDocumentView presents one authored Recipe to a check as named facts:
// whether the document declares a values overlay, and whether it declares a
// source pointer.
//
// It answers about the one document it was built for, which is the framework's
// contract rather than a simplification of it. The subject a check over a
// document is invoked with stands for the one document being presented rather
// than a member of a document set, because entity documents are validated one at
// a time as they are read. So the document argument each accessor takes names the
// document this value was built for, and asking about another is a mistake rather
// than a lookup that misses.
//
// It holds the answers rather than the Recipe, and that is the shape rather than
// a convenience. A view presents facts already produced; holding the document
// would leave every accessor free to read a field no fact declares, which is
// exactly the reach a view exists to close. Each answer is produced here by the
// one reader of its own question — the overlay's own emptiness test, and the
// source reader this package already uses everywhere it asks — so the fact stays
// single-valued rather than gaining a second derivation that agrees today.
//
// It satisfies the whole document-stage view, which declares facts a Recipe
// cannot carry: an Aggregate's text and parameter contract, an entity's
// inheritance chain, a Manifest's annotation bindings. The embedded base
// supplies those and refuses each at the point a check reads it, naming the
// document kind that does carry it. What holds this type to the facts a Recipe
// itself carries is the obligation asserted at the bottom of this file: a Recipe
// fact added to the registry stops this type compiling, and a fact carried by
// any other kind does not reach it.
type recipeDocumentView struct {
	// RecipeDocumentBase supplies the document-stage facts a Recipe does not
	// carry, and declares nothing for the ones it does — so this type writes
	// bodies for exactly the Recipe's own facts.
	constraint.RecipeDocumentBase

	// path is the workspace-relative file the document was read from. It is the
	// document coordinate itself, which is why an accessor's argument is checked
	// against it.
	path string

	// valuesMapping is whether the document declares a values overlay.
	valuesMapping bool

	// source is whether the document declares a source pointer.
	source bool
}

// newRecipeDocumentView presents the Recipe a load is holding: the file it was
// read from, and the two shape answers read out of that document.
//
// The answers are read here rather than taken from the caller because each has
// exactly one producer in this package, and routing the presentation through
// those producers is what keeps each fact single-valued. A caller passing its own
// booleans could pass ones read from a different document, or read differently.
func newRecipeDocumentView(path string, r *types.Recipe) recipeDocumentView {
	return recipeDocumentView{
		path:          path,
		valuesMapping: !r.Spec.ValuesMapping.IsEmpty(),
		source:        !sourceIsEmpty(r.Spec.Source),
	}
}

// RecipeValuesMappingDeclared reports whether the presented Recipe declares a
// values overlay.
func (v recipeDocumentView) RecipeValuesMappingDeclared(document string) bool {
	v.refuseOtherDocument(document, "RecipeValuesMappingDeclared")
	return v.valuesMapping
}

// RecipeSourceDeclared reports whether the presented Recipe declares a source
// pointer.
func (v recipeDocumentView) RecipeSourceDeclared(document string) bool {
	v.refuseOtherDocument(document, "RecipeSourceDeclared")
	return v.source
}

// refuseOtherDocument refuses an accessor call naming a document this value was
// not built for, because answering would hand back this document's fact under
// another document's name.
func (v recipeDocumentView) refuseOtherDocument(document, accessor string) {
	if document == v.path {
		return
	}
	panic(fmt.Sprintf(
		"recipeDocumentView.%s asked about document %q while presenting %q: "+
			"a view presents one document, so present the document being asked about",
		accessor, document, v.path))
}

// The compiler holds the type to the contract rather than a comment claiming it:
// a signature drifting from the declared facts stops compiling here.
//
// The second assertion is the half the embedded base cannot supply. Embedding
// makes every document-stage fact compile, so the view assertion alone would
// accept a presentation answering none of them; naming the kind's own obligation
// is what makes a Recipe fact added to the registry fail here, and on no
// presentation of another kind.
var (
	_ constraint.DocumentView        = recipeDocumentView{}
	_ constraint.RecipeDocumentFacts = recipeDocumentView{}
)
