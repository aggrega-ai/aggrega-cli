// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package documentview presents one authored Recipe or FeatureRecipe to a
// document-stage check as the named facts that kind carries.
//
// It is the one presentation of each kind, and it sits below every package that
// reads such a document so that each of them presents it through the same
// producers: the load-time shape rules and the document envelope's gate both
// present a Recipe here, and a fact one of them reads cannot come to be produced
// differently for the other.
package documentview

import (
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/sourceref"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcekind"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// Recipe presents one authored Recipe to a check as named facts:
// whether it declares a values overlay, a source pointer, a workload body or
// artifact entries, whether it writes the source entry at all, the kind its
// structured source names, the reference its source names, the keys each of its
// two output-label blocks declares, and the id each of its pipeline steps
// declares.
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
type Recipe struct {
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

	// sourceWritten is whether the document writes the source entry at all,
	// whatever it holds.
	sourceWritten bool

	// workload is whether the document declares a workload body.
	workload bool

	// artifacts is whether the document declares any artifact entry.
	artifacts bool

	// sourceKind is the kind the document's structured source names, and
	// sourceKindWritten whether it names one.
	sourceKind        string
	sourceKindWritten bool

	// sourceReference is the reference the document's source names, as authored,
	// and sourceReferenceInURLKey whether it sits under a structured source's
	// `url:` key rather than being the source itself.
	sourceReference         string
	sourceReferenceInURLKey bool

	// sourceFields is the text each key of the document's structured source
	// writes, as authored. It is only looked up by key, never iterated.
	sourceFields map[string]string

	// outputLabelKeys are the keys the literal output-label block declares, in
	// sorted order.
	outputLabelKeys []string

	// outputComponentLabelKeys are the label keys the promoted block declares,
	// in the order the author wrote them.
	outputComponentLabelKeys []string

	// pipelineStepIDs are the ids the pipeline's steps declare, one per step, in
	// the order the steps are written.
	pipelineStepIDs []string
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

// authoredPipelineStepIDs returns the id each step of a pipeline declares, in the
// order the steps are written, so the entry at a position is the id of the step
// at that position.
//
// It is the one reader of that question for both authored kinds that write a
// pipeline. It copies the ids out rather than handing back the steps, so a value
// presented to a rule is the ids and nothing else a step declares.
func authoredPipelineStepIDs(steps []types.PipelineStep) []string {
	if len(steps) == 0 {
		return nil
	}
	ids := make([]string, len(steps))
	for i, step := range steps {
		ids[i] = step.ID
	}
	return ids
}

// NewRecipe presents the Recipe a reader is holding: the file it was read from,
// and the answers read out of that document.
//
// The answers are read here rather than taken from the caller because each has
// exactly one producer in this package, and routing the presentation through
// those producers is what keeps each fact single-valued. A caller passing its own
// answers could pass ones read from a different document, or read differently.
func NewRecipe(path string, r *types.Recipe) Recipe {
	kind, written := authoredSourceKind(r.Spec.Source)
	reference, inURLKey := authoredSourceReference(r.Spec.Source)
	return Recipe{
		path:                     path,
		valuesMapping:            !r.Spec.ValuesMapping.IsEmpty(),
		source:                   sourceref.Declared(r.Spec.Source),
		sourceWritten:            sourceEntryWritten(r.Spec.Source),
		workload:                 !r.Spec.Workload.IsEmpty(),
		artifacts:                r.Spec.Artifacts.Len() > 0,
		sourceKind:               kind,
		sourceKindWritten:        written,
		sourceReference:          reference,
		sourceReferenceInURLKey:  inURLKey,
		sourceFields:             authoredSourceFields(r.Spec.Source),
		outputLabelKeys:          authoredOutputLabelKeys(r.Spec.OutputLabels),
		outputComponentLabelKeys: authoredOutputComponentLabelKeys(r.Spec.OutputComponentLabels),
		pipelineStepIDs:          authoredPipelineStepIDs(r.Spec.Pipeline),
	}
}

// sourceEntryWritten reports whether a Spec.Source field was written at all,
// whatever it holds. An absent entry and one written as null both decode to nil,
// and both are how an author declares no source.
//
// It answers presence and never emptiness: sourceref.Declared is the one reading
// of whether the entry names anything, and it answers alike for an absent entry
// and an empty string. The two readings disagree on exactly one input — an entry written
// and left empty — which is the one a rule telling them apart exists to find.
func sourceEntryWritten(src any) bool {
	return src != nil
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

// authoredSourceReference reads the reference a source names, as authored, and
// whether it is the structured form's `url:` key rather than the source itself.
// It is the one reader of that text for both authored kinds that write a source,
// and it reads through the walk every other reading of a source's reference uses
// (sourceref.Reference), so a rule over the reference and the fetch it names read
// the same text.
//
// A source of a shape that walk cannot read, and a structured source writing no
// `url:`, answer with no text.
func authoredSourceReference(src any) (string, bool) {
	reference, readable := sourceref.Reference(src)
	if !readable {
		return "", false
	}
	_, structured := src.(map[string]any)
	return reference, structured
}

// authoredSourceFields reads the text each key of a structured source writes, as
// authored. It is the one reader of those keys for both authored kinds that write a
// source. A value that is not a string is presented as its text, so a rule is
// handed what the author typed; a source written as a single URI writes no keys.
func authoredSourceFields(src any) map[string]string {
	structured, ok := src.(map[string]any)
	if !ok {
		return nil
	}
	fields := make(map[string]string, len(structured))
	for key, value := range structured {
		if value == nil {
			continue
		}
		text, isString := value.(string)
		if !isString {
			text = fmt.Sprint(value)
		}
		fields[key] = text
	}
	return fields
}

// RecipeValuesMappingDeclared reports whether the presented Recipe declares a
// values overlay.
func (v Recipe) RecipeValuesMappingDeclared(document string) bool {
	v.refuseOtherDocument(document, "RecipeValuesMappingDeclared")
	return v.valuesMapping
}

// RecipeSourceDeclared reports whether the presented Recipe declares a source
// pointer.
func (v Recipe) RecipeSourceDeclared(document string) bool {
	v.refuseOtherDocument(document, "RecipeSourceDeclared")
	return v.source
}

// RecipeSourceEntryWritten reports whether the presented Recipe writes the
// source entry at all, whatever the entry holds.
func (v Recipe) RecipeSourceEntryWritten(document string) bool {
	v.refuseOtherDocument(document, "RecipeSourceEntryWritten")
	return v.sourceWritten
}

// RecipeWorkloadDeclared reports whether the presented Recipe declares a
// workload body.
func (v Recipe) RecipeWorkloadDeclared(document string) bool {
	v.refuseOtherDocument(document, "RecipeWorkloadDeclared")
	return v.workload
}

// RecipeArtifactsDeclared reports whether the presented Recipe declares any
// artifact entry.
func (v Recipe) RecipeArtifactsDeclared(document string) bool {
	v.refuseOtherDocument(document, "RecipeArtifactsDeclared")
	return v.artifacts
}

// RecipeAuthoredSourceKind is the kind the presented Recipe's structured source
// names, as authored, and whether it names one.
func (v Recipe) RecipeAuthoredSourceKind(document string) (string, bool) {
	v.refuseOtherDocument(document, "RecipeAuthoredSourceKind")
	return v.sourceKind, v.sourceKindWritten
}

// RecipeAuthoredSourceReference is the reference the presented Recipe's source
// names, as authored, and whether it sits under the structured form's `url:` key.
func (v Recipe) RecipeAuthoredSourceReference(document string) (string, bool) {
	v.refuseOtherDocument(document, "RecipeAuthoredSourceReference")
	return v.sourceReference, v.sourceReferenceInURLKey
}

// RecipeAuthoredSourceField is the text the presented Recipe's structured source
// writes under key, as authored, and whether it writes that key.
func (v Recipe) RecipeAuthoredSourceField(document, key string) (string, bool) {
	v.refuseOtherDocument(document, "RecipeAuthoredSourceField")
	text, written := v.sourceFields[key]
	return text, written
}

// RecipeSourceKindOciModule reports whether kind is an OCI module kind and, if
// so, whether it takes `module_subdir`, read from the kinds' one declaration.
func (v Recipe) RecipeSourceKindOciModule(kind string) (takesModuleSubdir, isModuleKind bool) {
	return ociModuleRowOf(kind)
}

// ociModuleRowOf is the one reading of the OCI module kinds' declaration for both
// authored kinds that write a source.
func ociModuleRowOf(kind string) (takesModuleSubdir, isModuleKind bool) {
	row, found := sourcekind.OCIArtifactOf(kind)
	return row.TakesModuleSubdir, found
}

// RecipeSourceKindServed reports whether this program serves kind, read from the
// served set's one declaration.
func (v Recipe) RecipeSourceKindServed(kind string) bool {
	return sourcekind.Served(kind)
}

// RecipeOutputLabelKeys returns the keys the presented Recipe's literal
// output-label block declares, in sorted order.
//
// They are the keys as authored, whatever namespace they sit in, so a key
// claiming a reserved one is among them — which is the whole of what a rule over
// them reports on.
func (v Recipe) RecipeOutputLabelKeys(document string) []string {
	v.refuseOtherDocument(document, "RecipeOutputLabelKeys")
	return v.outputLabelKeys
}

// RecipeOutputComponentLabelKeys returns the component label keys the presented
// Recipe promotes onto its rendered output, in the order the author wrote them.
func (v Recipe) RecipeOutputComponentLabelKeys(document string) []string {
	v.refuseOtherDocument(document, "RecipeOutputComponentLabelKeys")
	return v.outputComponentLabelKeys
}

// RecipePipelineStepIds returns the ids the presented Recipe's pipeline steps
// declare, one per step, in the order the steps are written.
func (v Recipe) RecipePipelineStepIds(document string) []string {
	v.refuseOtherDocument(document, "RecipePipelineStepIds")
	return v.pipelineStepIDs
}

// refuseOtherDocument refuses an accessor call naming a document this value was
// not built for, because answering would hand back this document's fact under
// another document's name.
func (v Recipe) refuseOtherDocument(document, accessor string) {
	if document == v.path {
		return
	}
	panic(fmt.Sprintf(
		"Recipe.%s asked about document %q while presenting %q: "+
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
	_ constraint.DocumentView        = Recipe{}
	_ constraint.RecipeDocumentFacts = Recipe{}
)
