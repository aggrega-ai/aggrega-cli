// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The proofs in this file cover the two rules declared over an attachment template
// that reads its callers' render facts: one over a template producing the
// attachment's own resources while its FeatureRecipe does not declare it, and one
// over a pipeline step's input or gate, which never sees the callers.
//
// Every proof drives the PRODUCTION invocation site over the real presentation and
// the real enumeration, and each asserts against the condition's own accessor
// rather than against "an error" or a string. The axis is calibrated before any
// verdict is read: a rule reached by no subject raises nothing, which is
// indistinguishable from a rule that ran and approved.

const (
	callerFactsNode = "api"
	callerFactsName = "guard"
	callerFactsType = "core/caller-guard"
)

// callerFactsHalt drives the rule through its production invocation site over one
// node carrying one attachment bound to fr, and returns the halt.
func callerFactsHalt(t *testing.T, fr *types.FeatureRecipe) error {
	t.Helper()

	graph := &types.IRGraph{}
	graph.Spec.Nodes.Set(callerFactsNode, types.IRNode{})
	entries := types.OrderedMap[types.IRFeatureEntry]{}
	entries.Set(callerFactsName, types.IRFeatureEntry{Type: callerFactsType})
	graph.Spec.FeatureValuesMapping.Set(callerFactsNode, entries)

	hostRecipe := &types.Recipe{}
	hostRecipe.Metadata.Name = "api-recipe"
	hostRecipe.Metadata.Version = "1.0.0"
	hostRecipe.Spec.Capability = "helm"
	recipes := map[string]*types.Recipe{callerFactsNode: hostRecipe}

	lookup := RecipeLookup{
		FeatureRecipe: func(instanceName, featureType string) (*types.FeatureRecipe, bool) {
			if instanceName == callerFactsName && featureType == callerFactsType {
				return fr, true
			}
			return nil, false
		},
	}

	view := newDescribedView(
		context.Background(), graph, stubGraphFacts{},
		recipes, lookup, nil, newStepDescriptorMemo(), nil, nil,
		NodeRenderedNames{}, nil, nil, nil,
	)
	if view == nil {
		t.Fatal("the view was not built; there is nothing for the rule to read")
	}
	axes := newDescribedStageCoordinates(
		graph,
		localsAxis{nodes: []string{callerFactsNode}, attachments: []string{callerFactsName}},
		recipes, lookup, nil,
	)
	if axes == nil {
		t.Fatal("the enumeration refused to build over a graph and a bound Recipe")
	}
	subjects := axes.AttachmentTemplateFields(callerFactsNode, callerFactsName)
	if want := len(AttachmentTemplateFields(fr)); len(subjects) != want || want == 0 {
		t.Fatalf("the axis presents %d subject(s) for a FeatureRecipe writing %d; the verdict below "+
			"would certify nothing", len(subjects), want)
	}
	return runCallerRenderFactsRule(context.Background(), view, axes)
}

// callerFactsFR is a FeatureRecipe writing text into one template-bearing block.
func callerFactsFR(block, text string, declares bool) *types.FeatureRecipe {
	fr := attachmentRenderedNameFR("helm", block, text)
	fr.Metadata.Name = callerFactsName
	fr.Spec.ReadsCallerRenderFacts = declares
	return fr
}

// TestTheCallerRenderFactsRuleIsReachedAndHalts is the invocation proof: one
// template per row, driven through the production site, asserted against the
// condition's own accessor — and against the OTHER condition's, so a row proves
// which of the two rules owns its field rather than only that something halted.
//
// The reading rows cover the two ways a template reaches a caller's facts — off
// each connection inside a range, and off one connection picked by index — in
// every block the axis enumerates. A template producing the attachment's own
// resources halts with the undeclared condition and is admitted once the
// FeatureRecipe declares it. A pipeline step's input or gate halts with the step's
// own condition WHETHER OR NOT the FeatureRecipe declares it, because a step never
// sees the connections. The admitted rows are the controls: a template reading the
// connections without their facts, a template reading a `render_facts` field that
// is not beneath the connections, a step reading the host's own facts in full under
// either declaration, and the same template on a FeatureRecipe that renders once
// for a boundary, whose own surface refuses the reference already.
func TestTheCallerRenderFactsRuleIsReachedAndHalts(t *testing.T) {
	t.Parallel()

	const inRange = `{{ range .context.used_by }}{{ .render_facts }}{{ end }}`
	const byIndex = `{{ (index .context.used_by 0).render_facts.pod_labels }}`
	const hostOwn = `{{ range .context.render_facts.pod_labels }}{{ .name }}{{ end }}`

	undeclared := ErrCallerRenderFactsNotDeclared
	inAStep := ErrPipelineStepReadsCallerRenderFacts

	for _, tc := range []struct {
		name     string
		block    string
		text     string
		declares bool
		scope    string
		want     func() error // nil: admitted
		wantRef  string
		field    string
	}{
		{name: "an artifacts entry reading each caller's facts", block: "artifacts", text: inRange,
			want: undeclared, wantRef: ".render_facts", field: "artifacts[probe.yaml]"},
		{name: "a values mapping reading one caller's facts", block: "valuesMapping", text: byIndex,
			want: undeclared, wantRef: ".render_facts.pod_labels", field: "valuesMapping"},
		{name: "a workload reading each caller's facts", block: "workload", text: inRange,
			want: undeclared, wantRef: ".render_facts", field: "workload"},
		{name: "a pipeline step input reading each caller's facts, undeclared", block: "pipeline-with",
			text: inRange, want: inAStep, wantRef: ".render_facts", field: "pipeline[patch].with"},
		{name: "a pipeline step gate reading one caller's facts, undeclared", block: "pipeline-when",
			text: byIndex, want: inAStep, wantRef: ".render_facts.pod_labels", field: "pipeline[patch].when"},
		{name: "a pipeline step input reading each caller's facts under a FeatureRecipe that declares it",
			block: "pipeline-with", text: inRange, declares: true,
			want: inAStep, wantRef: ".render_facts", field: "pipeline[patch].with"},
		{name: "a pipeline step gate reading one caller's facts under a FeatureRecipe that declares it",
			block: "pipeline-when", text: byIndex, declares: true,
			want: inAStep, wantRef: ".render_facts.pod_labels", field: "pipeline[patch].when"},
		{name: "the same reading under a FeatureRecipe that declares it", block: "artifacts", text: inRange,
			declares: true},
		{name: "the same reading by index under a FeatureRecipe that declares it", block: "valuesMapping",
			text: byIndex, declares: true},
		{name: "a template reading the connections and not their facts", block: "artifacts",
			text: `{{ range .context.used_by }}{{ .component_id }}{{ end }}`},
		{name: "a template reading a render_facts field that is not beneath the connections", block: "artifacts",
			text: `{{ range .values.items }}{{ .render_facts }}{{ end }}`},
		{name: "a pipeline step input reading the host's own facts in full", block: "pipeline-with",
			text: hostOwn},
		{name: "a pipeline step gate reading the host's own facts in full under a FeatureRecipe that declares it",
			block: "pipeline-when", text: hostOwn, declares: true},
		{name: "a FeatureRecipe rendering once for a boundary is left to its own surface's rules",
			block: "artifacts", text: inRange, scope: executionScopeBoundary},
		{name: "a step of a FeatureRecipe rendering once for a boundary is left to its own surface's rules",
			block: "pipeline-with", text: inRange, scope: executionScopeBoundary},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fr := callerFactsFR(tc.block, tc.text, tc.declares)
			fr.Spec.ExecutionScope = tc.scope
			halt := callerFactsHalt(t, fr)

			if tc.want == nil {
				if halt != nil {
					t.Fatalf("the rule refused a template it admits: %v", halt)
				}
				return
			}
			if halt == nil {
				t.Fatalf("the rule admitted %s reading its callers' render facts", tc.field)
			}
			if !errors.Is(halt, tc.want()) {
				t.Fatalf("the refusal does not carry the condition this field owes: %v", halt)
			}
			other := inAStep
			if errors.Is(tc.want(), inAStep()) {
				other = undeclared
			}
			if errors.Is(halt, other()) {
				t.Errorf("the refusal also carries the other rule's condition, so one fault has two: %v", halt)
			}
			if !strings.Contains(halt.Error(), tc.wantRef) {
				t.Errorf("the refusal does not name the offending reference %q: %v", tc.wantRef, halt)
			}
			if !strings.Contains(halt.Error(), tc.field) {
				t.Errorf("the refusal does not name the field %q it was written in: %v", tc.field, halt)
			}
			t.Logf("REFUSED %s -> %v", tc.name, halt)
		})
	}
}

// TestThePipelineStepReadingFollowsTheLabelFamily holds the reading that tells a
// pipeline step's input and gate apart from the templates producing a document's
// own resources — the reading that decides which of the two rules above owns a
// field.
//
// A wrong answer either way is silent: a step's field read as a resource template
// is told to declare a key that would not make its read work, and a resource
// template read as a step's is refused a reading its declaration admits. The rows
// include an artifacts entry whose authored key spells a step's label and a step
// whose id carries the label grammar's own characters, because only the fixed text
// this package writes may decide the family.
func TestThePipelineStepReadingFollowsTheLabelFamily(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		label string
		step  bool
	}{
		{label: "valuesMapping", step: false},
		{label: "workload", step: false},
		{label: "artifacts[probe.yaml]", step: false},
		{label: "artifacts[pipeline[x].with]", step: false},
		{label: "pipeline[patch].with", step: true},
		{label: "pipeline[patch].when", step: true},
		{label: "pipeline[a].with].with", step: true},
	} {
		if got := RecipeTemplateFieldIsPipelineStep(tc.label); got != tc.step {
			t.Errorf("%q is a pipeline step's field=%v, want %v", tc.label, got, tc.step)
		}
	}

	// The reading is held to the labels the enumeration actually builds for an
	// attachment, so a change to the label grammar cannot leave the two
	// disagreeing while both keep compiling.
	fr := &types.FeatureRecipe{}
	fr.Metadata.Name = callerFactsName
	fr.Spec.ValuesMapping.Inline = "a"
	fr.Spec.Workload.Inline = "b"
	artifacts := types.OrderedMap[types.ArtifactEntry]{}
	artifacts.Set("pipeline[x].with", types.ArtifactEntry{Inline: "c"})
	fr.Spec.Artifacts = artifacts
	fr.Spec.Pipeline = []types.PipelineStep{{ID: "patch", With: "d", When: "e"}}

	fields := AttachmentTemplateFields(fr)
	if len(fields) != 5 {
		t.Fatalf("the enumeration built %d field(s) for a FeatureRecipe writing five; the walk below "+
			"would hold the reading to fewer labels than it claims", len(fields))
	}
	for _, field := range fields {
		wantStep := len(field.AuthoredKeys) > 0 && field.AuthoredKeys[0] == recipeFieldPipeline
		if got := RecipeTemplateFieldIsPipelineStep(field.Label); got != wantStep {
			t.Errorf("the enumeration built %q under .spec.%s, and the reading says pipeline step=%v",
				field.Label, strings.Join(field.AuthoredKeys, "."), got)
		}
	}
}
