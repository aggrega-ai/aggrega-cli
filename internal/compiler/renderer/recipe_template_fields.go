// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The authored blocks a Recipe writes an inline template field under. They are
// constants because two surfaces spell them: the enumeration below, which builds
// a field's label out of them, and the readings beside it, which tell one family
// of label from another. A spelling written out at each would agree on the day it
// was written and drift afterwards, and the drift would be silent — a reading
// that stopped recognising a family would treat that family's fields as belonging
// to another, and the fields would be read with the wrong delimiter pair, or
// judged against a context they are not evaluated in.
const (
	recipeFieldValuesMapping = "valuesMapping"
	recipeFieldWorkload      = "workload"
	recipeFieldArtifacts     = "artifacts"
	recipeFieldPipeline      = "pipeline"
	recipeFieldPipelineWith  = "with"
	recipeFieldPipelineWhen  = "when"
	recipeFieldOmits         = "omits"
	recipeFieldBoundary      = "boundary"

	// The fields of one omits entry, in the order the entry is evaluated in: its
	// gate first, then the ones it resolves to.
	omitFieldWhen      = "when"
	omitFieldKind      = "kind"
	omitFieldName      = "name"
	omitFieldNamespace = "namespace"
	omitFieldSubchart  = "subchart"
)

// artifactsLabelPrefix opens the label of every field written inside a Recipe's
// artifacts block, and nothing else opens with it: the labels below are built
// from the constants above, and each family's prefix is fixed text this file
// writes ahead of whatever the author typed. So the reading is sound whatever an
// author names an entry — a key carrying a bracket or a dot changes what follows
// the prefix and never the prefix itself.
const artifactsLabelPrefix = recipeFieldArtifacts + "["

// boundaryArtifactsLabelPrefix opens the label of every field written inside a
// Recipe's boundary block, the files it contributes to its boundary's shared
// output, on the reading [artifactsLabelPrefix] states. It opens with the block's
// own key, so neither family's prefix is a prefix of the other's.
const boundaryArtifactsLabelPrefix = recipeFieldBoundary + "." + recipeFieldArtifacts + "["

// pipelineLabelPrefix opens the label of every field written on a pipeline step —
// its input and its gate — and nothing else opens with it, on the reading
// [artifactsLabelPrefix] states: every family's prefix is fixed text this file
// writes ahead of whatever the author typed, so a step id or an artifact key
// spelling the same characters changes what follows a prefix and never the prefix.
const pipelineLabelPrefix = recipeFieldPipeline + "["

// omitFieldLabel names one field of one omits entry the way an author reads it back:
// the block, the entry's position in the list, and the field. It is the label the
// enumeration below presents the field under and the name the field's template is
// evaluated under, so a finding about the field and a failure evaluating it name it
// the same way.
//
// An omits entry is keyed by its position because the list carries no key of its
// own. The position counts every entry the author wrote, so an entry a when drops at
// evaluation is still counted and the label stays the one the document's own order
// gives it.
func omitFieldLabel(position int, field string) string {
	return fmt.Sprintf("%s[%d].%s", recipeFieldOmits, position, field)
}

// omitTemplateFields presents the template fields one omits list writes, each
// entry's when first and then its kind, name, namespace and subchart — the order the entry is
// evaluated in — under the labels [omitFieldLabel] spells and the authored keys that
// locate each field inside the document's spec.
//
// It is one body because two enumerations carry the list: the one a node's Recipe
// and a component-scope attachment's FeatureRecipe share, and the one a
// boundary-scope FeatureRecipe is read through. Each field of an entry is a template
// evaluated against the context the document's values mapping is evaluated against,
// so every rule invoked over those enumerations reads them as it reads that mapping.
func omitTemplateFields(entries []types.OmitEntry, add func(label, text string, keys ...string)) {
	for i, entry := range entries {
		position := strconv.Itoa(i)
		for _, field := range []struct{ name, text string }{
			{omitFieldWhen, entry.When},
			{omitFieldKind, entry.Kind},
			{omitFieldName, entry.Name},
			{omitFieldNamespace, entry.Namespace},
			{omitFieldSubchart, entry.Subchart},
		} {
			add(omitFieldLabel(i, field.name), field.text, recipeFieldOmits, position, field.name)
		}
	}
}

// RecipeTemplateField is one inline template field a Recipe declares: the label
// an author reads it back by, the text as written, and the authored keys beneath
// the document's spec that name it.
//
// The three travel together because they are three readings of one authored
// entry, and a surface deriving any of them from another would be free to differ
// from this one. The label is what a diagnostic quotes, the text is what a rule
// over authored text parses, and the keys are what position a finding inside the
// Recipe.
type RecipeTemplateField struct {
	// Label names the field the way an author reads it back: the authored key,
	// carrying the entry's own key or the step's id where the block is keyed.
	Label string

	// Text is the template as written, before anything is evaluated.
	Text string

	// AuthoredKeys are the keys beneath the Recipe's spec that name this field,
	// one segment per key.
	//
	// They are carried rather than derived from the label, and the difference is
	// not tidiness. Deriving them would mean parsing the label's grammar a second
	// time, in a second place, and the grammar joins an authored key to its block
	// with characters an authored key is not constrained to exclude — so an
	// artifact key or a step id containing one of them would not survive the round
	// trip, and the locator would name a path the file does not contain.
	AuthoredKeys []string
}

// RecipeTemplateFields presents every inline template field a Recipe declares
// whose text is evaluated against the node's own render context, as the one
// enumeration of that set.
//
// It is one home because three surfaces need the same answer: the axis a declared
// rule over these fields is invoked along, the presentation that hands one field's
// authored text to such a rule, and the reading that says which delimiter pair a
// field's text is parsed with. Two enumerations would be free to differ on which
// fields they carry, and a field either of them omitted would be silently
// unchecked — which is the failure an enumerated set exists to prevent.
//
// # What it covers, and what it deliberately leaves to another axis
//
// The set is the fields evaluated against the whole render context: the values
// mapping, the workload body, each artifacts entry, each field of each omits entry,
// each pipeline step's input and its gate, and each entry of the boundary block —
// the files the Recipe contributes to its boundary's shared output, which are
// evaluated against the context its own artifacts are. A Recipe's provides and
// locals entries are NOT here, and
// their absence is a decision rather than an omission: each already has its own
// axis and its own rules at this stage, so enumerating them again would report
// one authoring fault against two subjects and give a reader two positions for
// one edit. Its assertions are absent for a different reason — that surface is
// evaluated against a context carrying no identity block at all, so nothing about
// a node's own names is reachable from one.
//
// A field whose content is imported from a file or a library carries no text here
// and is absent from the set: that content is disk-resident or resolved when the
// render reaches it, so there is nothing to read before then. The inline forms are
// the whole of what this set covers, and a caller stating its coverage states that
// bound with it.
//
// The order is the document's own within each keyed block and fixed between the
// blocks, so a caller reporting the first offending field reports the same field
// on every run.
func RecipeTemplateFields(recipe *types.Recipe) []RecipeTemplateField {
	if recipe == nil {
		return nil
	}
	fields := inlineTemplateFieldsOf(
		recipe.Spec.ValuesMapping, recipe.Spec.Workload, recipe.Spec.Artifacts, recipe.Spec.Omits, recipe.Spec.Pipeline,
	)
	// The boundary block is a Recipe's alone — a FeatureRecipe carries none — so it
	// is enumerated here rather than in the body both kinds share.
	for k, v := range recipe.Spec.Boundary.Artifacts.All() {
		if v.Inline == "" {
			continue
		}
		fields = append(fields, RecipeTemplateField{
			Label:        boundaryArtifactsLabelPrefix + k + "]",
			Text:         v.Inline,
			AuthoredKeys: []string{recipeFieldBoundary, recipeFieldArtifacts, k},
		})
	}
	return fields
}

// AttachmentTemplateFields presents every inline template field the FeatureRecipe
// one per-component attachment binds declares whose text is evaluated against the
// attachment's render context, as the one enumeration of that set.
//
// # Why an attachment has its own enumeration rather than sharing the node's
//
// The set of BLOCKS is the same, and the body below is therefore shared — but the
// declaring document differs and the surface is a different reference set, so
// the two are separate readings of one body rather than one reading used twice. A
// caller asking about a node gets the Recipe's fields and a caller asking about an
// attachment gets that attachment's FeatureRecipe's, and neither can be handed the
// other's by mistake because the argument types differ.
//
// # What it covers, and what it deliberately leaves to another surface
//
// The set is the fields evaluated against the attachment's own render context: the
// values mapping, the workload body, each artifacts entry, each field of each omits
// entry, and each pipeline step's input and its gate. The FeatureRecipe's provides
// and locals entries are NOT here,
// for the reason a Recipe's are not on the node's own enumeration: each already has
// its own refusal at this stage — the scope those two surfaces are evaluated against
// narrows the reference directly — so enumerating them again would report one
// authoring fault against two subjects and give a reader two positions for one edit.
// Its inject mapping is absent for a different reason: it is evaluated against the
// consuming node's pipe rather than against this context, so a reference set derived
// from this one would be the wrong set for it.
//
// A field whose content is imported from a file or a library carries no text here
// and is absent from the set, exactly as it is on the node's own enumeration: that
// content is disk-resident or resolved when the render reaches it, so there is
// nothing to read before then. The inline forms are the whole of what this set
// covers, and a caller stating its coverage states that bound with it.
//
// The order is the document's own within each keyed block and fixed between the
// blocks, so a caller reporting the first offending field reports the same field on
// every run.
func AttachmentTemplateFields(fr *types.FeatureRecipe) []RecipeTemplateField {
	if fr == nil {
		return nil
	}
	return inlineTemplateFieldsOf(
		fr.Spec.ValuesMapping, fr.Spec.Workload, fr.Spec.Artifacts, fr.Spec.Omits, fr.Spec.Pipeline,
	)
}

// inlineTemplateFieldsOf builds the enumeration for one document's
// template-bearing blocks, in the fixed order every caller presents them in.
//
// It is one body because a Recipe and a FeatureRecipe declare these blocks
// identically — the same field types, the same authoring shapes, the same
// key-to-output rule — so what counts as an inline field, how its label is spelled
// and which authored keys locate it is one question with one answer. Two spellings
// of that would agree on the day they were written and drift afterwards, and the
// drift would be silent in the worst way available: a block one copy stopped
// enumerating would be a surface no rule was invoked over, which reports exactly
// what a rule that ran and approved reports.
//
// It takes the blocks rather than a document so that neither document type
// appears here. A signature naming one of them would make the other's call an
// adaptation of somebody else's reading; naming both through an interface would put
// a declaration in the way of a set whose whole point is that it is enumerated.
func inlineTemplateFieldsOf(
	valuesMapping types.ValuesMapping,
	workload types.WorkloadTemplate,
	artifacts types.OrderedMap[types.ArtifactEntry],
	omits []types.OmitEntry,
	pipeline []types.PipelineStep,
) []RecipeTemplateField {
	var out []RecipeTemplateField
	add := func(label, text string, keys ...string) {
		if text == "" {
			return
		}
		out = append(out, RecipeTemplateField{Label: label, Text: text, AuthoredKeys: keys})
	}
	add(recipeFieldValuesMapping, valuesMapping.Inline, recipeFieldValuesMapping)
	add(recipeFieldWorkload, workload.Inline, recipeFieldWorkload)
	for k, v := range artifacts.All() {
		add(artifactsLabelPrefix+k+"]", v.Inline, recipeFieldArtifacts, k)
	}
	omitTemplateFields(omits, add)
	for _, step := range pipeline {
		add(pipelineLabelPrefix+step.ID+"]."+recipeFieldPipelineWith,
			step.With, recipeFieldPipeline, step.ID, recipeFieldPipelineWith)
		add(pipelineLabelPrefix+step.ID+"]."+recipeFieldPipelineWhen,
			step.When, recipeFieldPipeline, step.ID, recipeFieldPipelineWhen)
	}
	return out
}

// RecipeImportFields presents every spec-root field a Recipe supplies by
// `$import:` rather than by writing the content itself, as the one enumeration of
// that set.
//
// It is the counterpart of [RecipeTemplateFields] and the two are disjoint by
// construction: the authoring surface lets a field be written inline or imported
// and admits no third state, so each of these fields lands on exactly one of the
// two enumerations. That is why a field is absent here the moment it carries
// inline text, and why nothing has to reconcile the two sets.
//
// # Why the artifacts block is not here
//
// An artifacts entry is also supplied by `$import:`, and it is deliberately left
// to its own enumeration: the artifacts block is keyed, so its coordinate is the
// entry's own key rather than a spec-root field name, and a rule over it is
// invoked once per entry. Folding both into one axis would hand the framework two
// coordinate shapes under one name.
//
// # What it does NOT decide
//
// It reports which fields carry an import, never whether the reference is one the
// compiler can resolve. A field naming a path in the workspace and one carrying a
// registry host are both present here, because both are imports the author wrote;
// which of them a given rule governs is settled by the facts declared over this
// coordinate and by the rule reading them, not by narrowing the axis. Narrowing it
// instead would make the axis mean one rule rather than the document.
//
// The order is fixed rather than the document's own, because a Recipe's spec-root
// keys are a fixed set: a caller reporting the first offending field reports the
// same field on every run.
func RecipeImportFields(recipe *types.Recipe) []string {
	if recipe == nil {
		return nil
	}
	return importFieldsOf(recipe.Spec.ValuesMapping, recipe.Spec.Workload)
}

// importFieldsOf is the body [RecipeImportFields] presents, over the two fields
// themselves rather than over a Recipe, so a FeatureRecipe — which authors both
// fields the same way — is enumerated by the same body.
func importFieldsOf(valuesMapping types.ValuesMapping, workload types.WorkloadTemplate) []string {
	var out []string
	if valuesMapping.Import != "" {
		out = append(out, recipeFieldValuesMapping)
	}
	if workload.Import != "" {
		out = append(out, recipeFieldWorkload)
	}
	return out
}

// RecipeTemplateFieldReadsWithArtifactDelimiters reports whether one enumerated
// field's text is parsed with the delimiter pair the node's provider declares for
// an artifact rather than with the pair every other authored template uses.
//
// It sits beside the enumeration that builds the label so the label's grammar has
// exactly one home: the family prefix is written here and recognised here, and a
// second reading elsewhere would be a second copy of that grammar.
//
// The distinction is what makes a reference set over one of these fields correct
// rather than approximate. A provider whose own target syntax uses the usual pair
// declares an alternate one for its artifacts, so an artifacts body read with the
// usual pair would surface the target's own syntax as references the author never
// made — and read with the alternate pair, a body written for any other field
// would surface none of the references it does make.
//
// A boundary block's entry is read with the artifact pair too, because it is
// evaluated by the walk an artifacts entry is, with the pair that walk uses.
func RecipeTemplateFieldReadsWithArtifactDelimiters(label string) bool {
	return strings.HasPrefix(label, artifactsLabelPrefix) || strings.HasPrefix(label, boundaryArtifactsLabelPrefix)
}

// RecipeTemplateFieldIsPipelineStep reports whether one enumerated field is a
// pipeline step's input or its gate rather than a template that produces the
// document's own resources.
//
// It sits beside the enumeration for the reason the delimiter reading above does:
// the label's grammar has one home, and the family prefix is written and
// recognised here.
//
// The distinction is what a reading of an attachment's templates needs where the
// two families are evaluated against different contexts. A step's templates run
// without the connections made to the attachment's host, because a step may
// rewrite the host's own documents; the templates producing the attachment's own
// resources run with them. So a reference to those connections means one thing in
// a step and another everywhere else, and a rule judging it has to know which
// family the field belongs to.
func RecipeTemplateFieldIsPipelineStep(label string) bool {
	return strings.HasPrefix(label, pipelineLabelPrefix)
}
