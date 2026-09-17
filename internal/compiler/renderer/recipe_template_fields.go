// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The authored blocks a Recipe writes an inline template field under. They are
// constants because two surfaces spell them: the enumeration below, which builds
// a field's label out of them, and the reading beside it, which tells one family
// of label from another. A spelling written out at each would agree on the day it
// was written and drift afterwards, and the drift would be silent — a reading
// that stopped recognising a family would treat that family's fields as belonging
// to another, and the fields would be read with the wrong delimiter pair.
const (
	recipeFieldValuesMapping = "valuesMapping"
	recipeFieldWorkload      = "workload"
	recipeFieldArtifacts     = "artifacts"
	recipeFieldPipeline      = "pipeline"
	recipeFieldPipelineWith  = "with"
	recipeFieldPipelineWhen  = "when"
)

// artifactsLabelPrefix opens the label of every field written inside a Recipe's
// artifacts block, and nothing else opens with it: the labels below are built
// from the constants above, and each family's prefix is fixed text this file
// writes ahead of whatever the author typed. So the reading is sound whatever an
// author names an entry — a key carrying a bracket or a dot changes what follows
// the prefix and never the prefix itself.
const artifactsLabelPrefix = recipeFieldArtifacts + "["

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
// mapping, the workload body, each artifacts entry, and each pipeline step's
// input and its gate. A Recipe's provides and locals entries are NOT here, and
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
	return inlineTemplateFieldsOf(
		recipe.Spec.ValuesMapping, recipe.Spec.Workload, recipe.Spec.Artifacts, recipe.Spec.Pipeline,
	)
}

// AttachmentTemplateFields presents every inline template field the FeatureRecipe
// one per-component attachment binds declares whose text is evaluated against the
// attachment's render context, as the one enumeration of that set.
//
// # Why an attachment has its own enumeration rather than sharing the node's
//
// The set of BLOCKS is the same, and the body below is therefore shared — but the
// carrier is a different document and the surface is a different reference set, so
// the two are separate readings of one body rather than one reading used twice. A
// caller asking about a node gets the Recipe's fields and a caller asking about an
// attachment gets that attachment's FeatureRecipe's, and neither can be handed the
// other's by mistake because the argument types differ.
//
// # What it covers, and what it deliberately leaves to another surface
//
// The set is the fields evaluated against the attachment's own render context: the
// values mapping, the workload body, each artifacts entry, and each pipeline step's
// input and its gate. The FeatureRecipe's provides and locals entries are NOT here,
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
		fr.Spec.ValuesMapping, fr.Spec.Workload, fr.Spec.Artifacts, fr.Spec.Pipeline,
	)
}

// inlineTemplateFieldsOf builds the enumeration for one carrier's four
// template-bearing blocks, in the fixed order every caller presents them in.
//
// It is one body because a Recipe and a FeatureRecipe declare these four blocks
// identically — the same field types, the same three authoring modes, the same
// key-to-output rule — so what counts as an inline field, how its label is spelled
// and which authored keys locate it is one question with one answer. Two spellings
// of that would agree on the day they were written and drift afterwards, and the
// drift would be silent in the worst way available: a block one copy stopped
// enumerating would be a surface no rule was invoked over, which reports exactly
// what a rule that ran and approved reports.
//
// It takes the four blocks rather than a carrier so that neither carrier type
// appears here. A signature naming one of them would make the other's call an
// adaptation of somebody else's reading; naming both through an interface would put
// a declaration in the way of a set whose whole point is that it is enumerated.
func inlineTemplateFieldsOf(
	valuesMapping types.ValuesMapping,
	workload types.WorkloadTemplate,
	artifacts types.OrderedMap[types.ArtifactEntry],
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
	for _, step := range pipeline {
		add(recipeFieldPipeline+"["+step.ID+"]."+recipeFieldPipelineWith,
			step.With, recipeFieldPipeline, step.ID, recipeFieldPipelineWith)
		add(recipeFieldPipeline+"["+step.ID+"]."+recipeFieldPipelineWhen,
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
	var out []string
	if recipe.Spec.ValuesMapping.Import != "" {
		out = append(out, recipeFieldValuesMapping)
	}
	if recipe.Spec.Workload.Import != "" {
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
func RecipeTemplateFieldReadsWithArtifactDelimiters(label string) bool {
	return strings.HasPrefix(label, artifactsLabelPrefix)
}
