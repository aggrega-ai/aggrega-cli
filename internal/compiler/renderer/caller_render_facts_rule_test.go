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

// The proofs in this file cover the rule declared over an attachment template that
// reads its callers' render facts while its FeatureRecipe does not declare it.
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
// condition's own accessor.
//
// The reading rows cover the two ways a template reaches a caller's facts — off
// each connection inside a range, and off one connection picked by index — in
// every block the axis enumerates. The admitted rows are the controls: the same
// template under a FeatureRecipe that declares it, a template reading the
// connections without their facts, a template reading a `render_facts` field that
// is not beneath the connections, and the same template on a FeatureRecipe that
// renders once for a boundary, whose own surface refuses the reference already.
func TestTheCallerRenderFactsRuleIsReachedAndHalts(t *testing.T) {
	t.Parallel()

	const inRange = `{{ range .context.used_by }}{{ .render_facts }}{{ end }}`
	const byIndex = `{{ (index .context.used_by 0).render_facts.pod_labels }}`

	for _, tc := range []struct {
		name     string
		block    string
		text     string
		declares bool
		scope    string
		wantHalt bool
		wantRef  string
		field    string
	}{
		{name: "an artifacts entry reading each caller's facts", block: "artifacts", text: inRange,
			wantHalt: true, wantRef: ".render_facts", field: "artifacts[probe.yaml]"},
		{name: "a values mapping reading one caller's facts", block: "valuesMapping", text: byIndex,
			wantHalt: true, wantRef: ".render_facts.pod_labels", field: "valuesMapping"},
		{name: "a workload reading each caller's facts", block: "workload", text: inRange,
			wantHalt: true, wantRef: ".render_facts", field: "workload"},
		{name: "a pipeline step input reading each caller's facts", block: "pipeline-with", text: inRange,
			wantHalt: true, wantRef: ".render_facts", field: "pipeline[patch].with"},
		{name: "a pipeline step gate reading one caller's facts", block: "pipeline-when", text: byIndex,
			wantHalt: true, wantRef: ".render_facts.pod_labels", field: "pipeline[patch].when"},
		{name: "the same reading under a FeatureRecipe that declares it", block: "artifacts", text: inRange,
			declares: true},
		{name: "the same reading by index under a FeatureRecipe that declares it", block: "valuesMapping",
			text: byIndex, declares: true},
		{name: "a template reading the connections and not their facts", block: "artifacts",
			text: `{{ range .context.used_by }}{{ .component_id }}{{ end }}`},
		{name: "a template reading a render_facts field that is not beneath the connections", block: "artifacts",
			text: `{{ range .values.items }}{{ .render_facts }}{{ end }}`},
		{name: "a FeatureRecipe rendering once for a boundary is left to its own surface's rules",
			block: "artifacts", text: inRange, scope: executionScopeBoundary},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fr := callerFactsFR(tc.block, tc.text, tc.declares)
			fr.Spec.ExecutionScope = tc.scope
			halt := callerFactsHalt(t, fr)

			if !tc.wantHalt {
				if halt != nil {
					t.Fatalf("the rule refused a template it admits: %v", halt)
				}
				return
			}
			if halt == nil {
				t.Fatalf("the rule admitted %s reading its callers' render facts undeclared", tc.field)
			}
			if !errors.Is(halt, ErrCallerRenderFactsNotDeclared()) {
				t.Fatalf("the refusal does not carry the condition: %v", halt)
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
