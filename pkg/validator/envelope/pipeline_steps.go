// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package envelope

import (
	"fmt"

	"go.yaml.in/yaml/v3"

	aggerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// ErrDuplicatePipelineStepID is the sentinel for a Recipe or FeatureRecipe
// whose `spec.pipeline` declares one step id on two of its steps. A step
// publishes its outputs under its id for the steps after it to read, so two
// steps sharing one id would leave a later step reading whichever of the two
// published last rather than the one its author meant.
//
// It is raised by this package's gate, after the document has passed its
// kind's schema: JSON Schema has no keyword for a list whose entries are unique
// by one of their properties, so the schema accepts such a document and the
// rule is applied here instead. The violation is reported as an *Error, so it
// carries the file, one finding per repeated id positioned at the repeat, and
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

// pipelineStepIDsUnique refuses a Recipe or FeatureRecipe whose pipeline
// declares one step id twice, and returns nil for every other document.
//
// instance is the normalised generic document the schema has already accepted,
// so the pipeline, where present, is a list of mappings each carrying a string
// id. The steps are read in the order they are authored, and each repeat names
// the first step that declared its id, so the findings follow the document and
// two runs over one document report the same thing in the same order.
func pipelineStepIDsUnique(key compiledKey, instance any, path string, data []byte) error {
	if key.kind != KindRecipe && key.kind != KindFeatureRecipe {
		return nil
	}
	root, _ := instance.(map[string]any)
	spec, _ := root["spec"].(map[string]any)
	steps, _ := spec["pipeline"].([]any)
	if len(steps) < 2 {
		return nil
	}
	type repeat struct {
		id           string
		first, again int
	}
	firstAt := make(map[string]int, len(steps))
	var repeats []repeat
	for i, s := range steps {
		step, _ := s.(map[string]any)
		id, ok := step["id"].(string)
		if !ok {
			continue
		}
		if first, seen := firstAt[id]; seen {
			repeats = append(repeats, repeat{id: id, first: first, again: i})
			continue
		}
		firstAt[id] = i
	}
	if len(repeats) == 0 {
		return nil
	}

	e := &Error{
		Kind:      key.kind,
		Path:      path,
		condition: ErrDuplicatePipelineStepID(),
		advice:    adviceDistinctStepIDs,
	}
	locate := stepIDLocator(data, path)
	e.Positions = make(map[string]aggerrors.SourceLocation, 2*len(repeats))
	for _, r := range repeats {
		firstPath := stepIDPath(r.first)
		againPath := stepIDPath(r.again)
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
		msg := fmt.Sprintf("step id %q is already declared at %s", r.id, where)
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

// stepIDLocator returns a lookup from a field path to its position in data,
// resolved through the same walk and in the same unit — characters — as a
// schema finding's position. A document that does not re-parse as a node tree
// yields a lookup that resolves nothing, and its findings render without a
// position rather than with one a reader would have to distrust.
func stepIDLocator(data []byte, path string) func(string) (aggerrors.SourceLocation, bool) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return func(string) (aggerrors.SourceLocation, bool) { return aggerrors.SourceLocation{}, false }
	}
	idx := yamlpos.BuildLineIndexWithEncoding(data, yamlpos.PositionEncodingUTF32)
	return func(fieldPath string) (aggerrors.SourceLocation, bool) {
		span, ok := yamlpos.FieldRange(&doc, idx, fieldPath)
		if !ok {
			return aggerrors.SourceLocation{}, false
		}
		return aggerrors.SourceLocation{File: path, Line: span.Line, Column: span.Column}, true
	}
}
