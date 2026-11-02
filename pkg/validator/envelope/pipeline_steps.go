// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package envelope

import (
	"fmt"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/documentview"
	aggerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
)

// ErrDuplicatePipelineStepID is the sentinel for a Recipe or FeatureRecipe
// whose `spec.pipeline` declares one step id on two of its steps. A step
// publishes its outputs under its id for the steps after it to read, so two
// steps sharing one id would leave a later step reading whichever of the two
// published last rather than the one its author meant.
//
// It is asked by this package's gate, after the document has passed its kind's
// schema: JSON Schema has no keyword for a list whose entries are unique by one
// of their properties, so the schema accepts such a document and the rule is
// applied here instead. The rule itself is declared as a check for each of the
// two kinds that write a pipeline — recipePipelineStepIDsAreUnique and
// featureRecipePipelineStepIDsAreUnique — and the gate presents the document
// and invokes the one for its kind. The violation is reported as an *Error, so
// it carries the file, one finding per repeated id positioned at the repeat, and
// the position of the step that declared the id first; and it matches
// ErrEntityEnvelope as well as this sentinel, so a caller gating on the gate's
// one sentinel still catches it.
//
// The rule covers one document's own pipeline and nothing wider. A node runs
// its Recipe's pipeline and the pipelines of the FeatureRecipes attached to it
// as separate walks, each starting with no step outputs, so an id one of them
// shares with another is not refused.
func ErrDuplicatePipelineStepID() error { return errDuplicatePipelineStepID }

var errDuplicatePipelineStepID = aggerrors.Register("duplicate-pipeline-step-id", aggerrors.Meta{
	ShortCode: "E0370",
	Message:   "a pipeline declares one step id twice",
	RulePlace: aggerrors.RuleOverAuthoredInput(),
})

// adviceDistinctStepIDs is the advice for a repeated step id.
const adviceDistinctStepIDs = "give each step of the pipeline its own id, and point every `.steps.<id>` reference a later step makes at the step it means"

// repeatedPipelineStepIDs is the rule itself: each step of one pipeline declares
// an id no earlier step of that pipeline declared.
//
// ids are the steps' ids in the order the steps are written, so the entry at a
// position is the id of the step there. They are read in that order, and each
// repeat names the first step that declared its id, so the findings follow the
// document and two runs over one document report the same thing in the same
// order. An id appearing three times yields two objections, each naming the
// first.
//
// Each objection carries three values, in this order, and the gate renders them
// by that order: the repeated id, the field path of the repeat, and the field
// path of the first declaration. The paths are spelled here, by stepIDPath, so
// the step a rule names and the step a finding is positioned at are one string.
//
// It is one function over both authored kinds that write a pipeline, and the
// two declarations below hand it the ids each was invoked for. The rule does not
// vary by kind; which document the ids were read from is the document-kind axis
// the two declarations express.
func repeatedPipelineStepIDs(s constraint.DocumentSubject, ids []string) []constraint.Objection {
	firstAt := make(map[string]int, len(ids))
	var out []constraint.Objection
	for i, id := range ids {
		if first, seen := firstAt[id]; seen {
			out = append(out, s.RaiseWithAdvice(
				ErrDuplicatePipelineStepID(),
				adviceDistinctStepIDs,
				constraint.Uncaptioned(id),
				constraint.Captioned("declared again at", stepIDPath(i)),
				constraint.Captioned("first declared at", stepIDPath(first)),
			))
			continue
		}
		firstAt[id] = i
	}
	return out
}

// recipePipelineStepIDsAreUnique refuses a Recipe whose pipeline declares one
// step id on two of its steps.
//
// It is one of two declarations of one rule, and the plurality is forced by the
// check type rather than chosen: a check reads one presentation, and the two
// authored kinds that write a pipeline are two document kinds whose facts are
// filed apart. Both raise the same condition, so the diagnostic an author reads
// does not turn on which kind they wrote.
var recipePipelineStepIDsAreUnique = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentSubject,
]{
	Sentinel: ErrDuplicatePipelineStepID(),
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.DocumentView, s constraint.DocumentSubject) []constraint.Objection {
		return repeatedPipelineStepIDs(s, v.RecipePipelineStepIds(s.SourceFile()))
	},
}

// featureRecipePipelineStepIDsAreUnique refuses a FeatureRecipe whose pipeline
// declares one step id on two of its steps.
//
// It is the FeatureRecipe document's declaration of the rule
// recipePipelineStepIDsAreUnique declares for a Recipe. Why there are two is
// stated there; it is not repeated here, because a rule's reasoning has one home.
var featureRecipePipelineStepIDsAreUnique = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentSubject,
]{
	Sentinel: ErrDuplicatePipelineStepID(),
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.DocumentView, s constraint.DocumentSubject) []constraint.Objection {
		return repeatedPipelineStepIDs(s, v.FeatureRecipePipelineStepIds(s.SourceFile()))
	},
}

// pipelineStepIDObjections presents a Recipe or FeatureRecipe document and
// invokes the step-id rule declared for its kind, returning what the rule raised;
// every other kind is not asked and yields nothing.
//
// The document is presented from data decoded into its kind's type, exactly as a
// catalog load decodes it, so the facts the rule reads are produced by the same
// producers every other reading of that document uses. A document that does not
// decode is not asked: the gate has already accepted its shape, so what refuses
// it is whichever reading next decodes it, and this rule adds no refusal of its
// own in front of that one.
func pipelineStepIDObjections(kind Kind, path string, data []byte) []constraint.Objection {
	switch kind {
	case KindRecipe:
		var r types.Recipe
		if err := yaml.Unmarshal(data, &r); err != nil {
			return nil
		}
		// Held as the view the rule declared rather than as the concrete type,
		// because the entry infers one type from the check and the view together.
		var view constraint.DocumentView = documentview.NewRecipe(path, &r)
		return constraint.RunOnDocument(path, view, recipePipelineStepIDsAreUnique)
	case KindFeatureRecipe:
		var fr types.FeatureRecipe
		if err := yaml.Unmarshal(data, &fr); err != nil {
			return nil
		}
		var view constraint.DocumentView = documentview.NewFeatureRecipe(path, &fr)
		return constraint.RunOnDocument(path, view, featureRecipePipelineStepIDsAreUnique)
	}
	return nil
}

// pipelineStepIDsUnique refuses a Recipe or FeatureRecipe whose pipeline
// declares one step id twice, and returns nil for every other document.
//
// It renders what the rule raised into this package's *Error: one finding per
// repeat, positioned at the repeated id, its sentence naming the step that
// declared the id first together with that step's position. The condition and
// the advice are read off the objections rather than named here, because the
// rule is what raised them.
func pipelineStepIDsUnique(key compiledKey, path string, data []byte) error {
	objections := pipelineStepIDObjections(key.kind, path, data)
	if len(objections) == 0 {
		return nil
	}

	e := &Error{
		Kind:      key.kind,
		Path:      path,
		condition: objections[0].Sentinel(),
		advice:    objections[0].Advice(),
	}
	locate, _ := fieldLocator(data, path)
	e.Positions = make(map[string]aggerrors.SourceLocation, 2*len(objections))
	for _, o := range objections {
		// The rule supplies the repeated id, the repeat's path and the first
		// declaration's path, in that order; see repeatedPipelineStepIDs.
		detail := o.Detail()
		id, againPath, firstPath := detail[0], detail[1], detail[2]
		where := firstPath
		if loc, ok := locate(firstPath); ok {
			e.Positions[firstPath] = loc
			where = fmt.Sprintf("%s (line %d, column %d)", firstPath, loc.Line, loc.Column)
		}
		if loc, ok := locate(againPath); ok {
			e.Positions[againPath] = loc
		}
		// A document validated through the withholding entry point may carry
		// values produced from data an author declared sensitive, and which of
		// them did cannot be told, so the id is not quoted there.
		msg := fmt.Sprintf("step id %q is already declared at %s", id, where)
		if key.withholdEvery != "" {
			msg = fmt.Sprintf("this step's id is already declared at %s%s", where, key.withholdEvery)
		}
		e.Findings = append(e.Findings, jsonvalidator.Finding{FieldPath: againPath, Message: msg})
	}
	return e
}

// stepIDPath spells the field path of the id of the step at index i, in the
// spelling the schema engine's findings use and yamlpos resolves.
func stepIDPath(i int) string { return fmt.Sprintf("spec.pipeline[%d].id", i) }
