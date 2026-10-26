// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcekind"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// recipeDocumentView presents one authored Recipe to a check as named facts:
// the capability the document declares, whether it declares a values overlay, a
// source pointer, a workload body or artifact entries, and the keys each of its
// two output-label blocks declares.
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
// one reader of its own question — each block's own emptiness test, and the
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

	// workload is whether the document declares a workload body.
	workload bool

	// artifacts is whether the document declares any artifact entry.
	artifacts bool

	// capability is the capability the document declares, as authored.
	capability string

	// sourceKind is the kind the document's structured source names, and
	// sourceKindWritten whether it names one.
	sourceKind        string
	sourceKindWritten bool

	// outputLabelKeys are the keys the literal output-label block declares, in
	// sorted order.
	outputLabelKeys []string

	// outputComponentLabelKeys are the label keys the promoted block declares,
	// in the order the author wrote them.
	outputComponentLabelKeys []string
}

// authoredOutputLabelKeys returns the keys a literal output-label block
// declares, sorted.
//
// The sort is here rather than at a rule, because the order is a property of the
// fact this produces: the block is a mapping, whose iteration order the language
// leaves unspecified, so an unsorted walk would present one authored document's
// keys in a different order on different runs and the first finding a rule
// reported would move with it. Sorting once, where the fact is produced, is what
// makes the answer the same answer every time — and it is the only place that can
// hold it, since a rule invoked once per key never sees the set.
func authoredOutputLabelKeys(block map[string]string) []string {
	keys := make([]string, 0, len(block))
	for key := range block {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// authoredOutputComponentLabelKeys returns the label keys a promoted block
// declares, in the order the author wrote them.
//
// It copies rather than handing back the authored slice, so a value presented to
// a rule cannot be reordered through the presentation. Nothing sorts here: the
// block is a sequence, so the author's order is the one it carries and the one a
// rule's findings appear in.
func authoredOutputComponentLabelKeys(block []string) []string {
	if len(block) == 0 {
		return nil
	}
	keys := make([]string, len(block))
	copy(keys, block)
	return keys
}

// newRecipeDocumentView presents the Recipe a load is holding: the file it was
// read from, and the answers read out of that document.
//
// The answers are read here rather than taken from the caller because each has
// exactly one producer in this package, and routing the presentation through
// those producers is what keeps each fact single-valued. A caller passing its own
// answers could pass ones read from a different document, or read differently.
//
// The capability is carried as authored and is not compared against anything
// here. A view presents what the document says; which capability a rule cares
// about is the rule's own business, and a presentation that answered "is this an
// eval Recipe" would be presenting one rule's predicate as though it were a
// property of the file.
func newRecipeDocumentView(path string, r *types.Recipe) recipeDocumentView {
	kind, written := authoredSourceKind(r.Spec.Source)
	return recipeDocumentView{
		path:                     path,
		valuesMapping:            !r.Spec.ValuesMapping.IsEmpty(),
		source:                   !sourceIsEmpty(r.Spec.Source),
		workload:                 !r.Spec.Workload.IsEmpty(),
		artifacts:                r.Spec.Artifacts.Len() > 0,
		capability:               r.Spec.Capability,
		sourceKind:               kind,
		sourceKindWritten:        written,
		outputLabelKeys:          authoredOutputLabelKeys(r.Spec.OutputLabels),
		outputComponentLabelKeys: authoredOutputComponentLabelKeys(r.Spec.OutputComponentLabels),
	}
}

// authoredSourceKind reads the kind a structured source names under its `kind:`
// key, and whether it names one. It is the one reader of that word for both
// authored kinds that write a source.
//
// A source written as a single URI names no kind here: its kind is read from the
// URI's own shape where the source is resolved, so there is no authored word to
// present. Nor does a structured source whose `kind:` is absent or empty, because
// that is how an author asks for the same reading of the URL. A value that is not a
// string is presented as its text, so a rule is handed what the author typed.
func authoredSourceKind(src any) (string, bool) {
	structured, ok := src.(map[string]any)
	if !ok {
		return "", false
	}
	value, present := structured["kind"]
	if !present || value == nil {
		return "", false
	}
	kind, isString := value.(string)
	if !isString {
		kind = fmt.Sprint(value)
	}
	if kind == "" {
		return "", false
	}
	return kind, true
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

// RecipeWorkloadDeclared reports whether the presented Recipe declares a
// workload body.
func (v recipeDocumentView) RecipeWorkloadDeclared(document string) bool {
	v.refuseOtherDocument(document, "RecipeWorkloadDeclared")
	return v.workload
}

// RecipeArtifactsDeclared reports whether the presented Recipe declares any
// artifact entry.
func (v recipeDocumentView) RecipeArtifactsDeclared(document string) bool {
	v.refuseOtherDocument(document, "RecipeArtifactsDeclared")
	return v.artifacts
}

// RecipeDeclaredCapability is the capability the presented Recipe declares, as
// the author wrote it.
func (v recipeDocumentView) RecipeDeclaredCapability(document string) string {
	v.refuseOtherDocument(document, "RecipeDeclaredCapability")
	return v.capability
}

// RecipeAuthoredSourceKind is the kind the presented Recipe's structured source
// names, as authored, and whether it names one.
func (v recipeDocumentView) RecipeAuthoredSourceKind(document string) (string, bool) {
	v.refuseOtherDocument(document, "RecipeAuthoredSourceKind")
	return v.sourceKind, v.sourceKindWritten
}

// RecipeSourceKindServed reports whether this program serves kind, read from the
// served set's one declaration.
func (v recipeDocumentView) RecipeSourceKindServed(kind string) bool {
	return sourcekind.Served(kind)
}

// RecipeOutputLabelKeys returns the keys the presented Recipe's literal
// output-label block declares, in sorted order.
//
// They are the keys as authored, whatever namespace they sit in, so a key
// claiming a reserved one is among them — which is the whole of what a rule over
// them reports on.
func (v recipeDocumentView) RecipeOutputLabelKeys(document string) []string {
	v.refuseOtherDocument(document, "RecipeOutputLabelKeys")
	return v.outputLabelKeys
}

// RecipeOutputComponentLabelKeys returns the component label keys the presented
// Recipe promotes onto its rendered output, in the order the author wrote them.
func (v recipeDocumentView) RecipeOutputComponentLabelKeys(document string) []string {
	v.refuseOtherDocument(document, "RecipeOutputComponentLabelKeys")
	return v.outputComponentLabelKeys
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
