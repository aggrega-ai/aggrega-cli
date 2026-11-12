// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The proofs in this file cover the two rules declared over an attachment template
// that reads the render facts of the components its host is wired to — the twins
// of the callers' pair — and they are proven the way those are: through the
// production invocation site, over the real presentation and the real enumeration,
// asserted against each condition's own accessor.

// targetFactsHalt drives the targets' rules through their production invocation
// site over one node carrying one attachment bound to fr, and returns the halt.
func targetFactsHalt(t *testing.T, fr *types.FeatureRecipe, factory EngineFactory) error {
	t.Helper()
	return connectionFactsHalt(t, fr, factory, runTargetRenderFactsRule)
}

// targetFactsFR is a FeatureRecipe writing text into one template-bearing block,
// declaring the targets' read or not, and declaring the callers' read or not.
func targetFactsFR(block, text string, declaresTargets, declaresCallers bool) *types.FeatureRecipe {
	fr := callerFactsFR(block, text, declaresCallers)
	fr.Spec.ReadsTargetRenderFacts = declaresTargets
	return fr
}

// TestTheTargetRenderFactsRuleIsReachedAndHalts is the invocation proof: one
// template per row, driven through the production site, asserted against the
// condition's own accessor — and against every other condition over a connection's
// facts, so a row proves which rule owns its field rather than only that something
// halted.
//
// The reading rows cover the ways a template reaches a target's facts — beneath one
// alias written out, off each connection inside a range, off one connection picked
// by index, and off one connection a with names — in every block the axis
// enumerates. A template producing the attachment's own resources halts with the
// undeclared condition and is admitted once the FeatureRecipe declares the targets'
// read; declaring the callers' read instead does not admit it. A pipeline step's
// input or gate halts with the step's own condition whether or not the FeatureRecipe
// declares it, because a step is never handed those facts.
//
// The admitted rows are the controls: a template reading the connections without
// their facts, a template naming a connection whose alias is the field's own name, a
// template reading a `render_facts` field off something that is not the connections,
// the host's own facts read in full, a template reading a CALLER's facts (the other
// pair's subject, which this pair must leave alone), and the same reading on a
// FeatureRecipe that renders once for a boundary, whose own surface refuses the
// reference already.
//
// Two rows bind the FeatureRecipe's capability to a provider declaring its own
// delimiter pair for artifacts entries, so the pair an entry is read with is
// observable: a reading spelled in that pair halts, and the usual pair's spelling of
// the same reading is plain text there and is admitted.
func TestTheTargetRenderFactsRuleIsReachedAndHalts(t *testing.T) {
	t.Parallel()

	const byAlias = `{{ range .context.uses.db.render_facts.pod_template_labels }}{{ .name }}{{ end }}`
	const inRange = `{{ range .context.uses }}{{ .render_facts }}{{ end }}`
	const byIndex = `{{ (index .context.uses "db").render_facts.container_ports }}`
	const byWith = `{{ with .context.uses.db }}{{ .render_facts.controller_selectors }}{{ end }}`
	const hostOwn = `{{ range .context.render_facts.pod_template_labels }}{{ .name }}{{ end }}`
	const callersFacts = `{{ range .context.used_by }}{{ .render_facts }}{{ end }}`
	const inRangeAlternatePair = `[[ range .context.uses ]][[ .render_facts ]][[ end ]]`
	alternatePair := [2]string{"[[", "]]"}

	undeclared := ErrTargetRenderFactsNotDeclared
	inAStep := ErrPipelineStepReadsTargetRenderFacts
	others := []func() error{
		ErrTargetRenderFactsNotDeclared, ErrPipelineStepReadsTargetRenderFacts,
		ErrCallerRenderFactsNotDeclared, ErrPipelineStepReadsCallerRenderFacts,
	}

	for _, tc := range []struct {
		name            string
		block           string
		text            string
		declaresTargets bool
		declaresCallers bool
		scope           string
		pair            [2]string    // the artifacts pair the capability's provider declares; zero: no provider
		want            func() error // nil: admitted
		wantRef         string
		field           string
	}{
		{name: "an artifacts entry reading each target's facts in the pair its provider declares",
			block: "artifacts", text: inRangeAlternatePair, pair: alternatePair,
			want: undeclared, wantRef: ".render_facts", field: "artifacts[probe.yaml]"},
		{name: "the usual pair's spelling of that reading is text under a provider declaring another pair",
			block: "artifacts", text: inRange, pair: alternatePair},
		{name: "an artifacts entry reading one target's facts beneath its alias", block: "artifacts", text: byAlias,
			want: undeclared, wantRef: ".context.uses.db.render_facts.pod_template_labels", field: "artifacts[probe.yaml]"},
		{name: "an artifacts entry reading each target's facts", block: "artifacts", text: inRange,
			want: undeclared, wantRef: ".render_facts", field: "artifacts[probe.yaml]"},
		{name: "a values mapping reading one target's facts by index", block: "valuesMapping", text: byIndex,
			want: undeclared, wantRef: ".render_facts.container_ports", field: "valuesMapping"},
		{name: "a workload reading one target's facts through with", block: "workload", text: byWith,
			want: undeclared, wantRef: ".render_facts.controller_selectors", field: "workload"},
		{name: "declaring the callers' read does not admit a target's facts", block: "artifacts", text: byAlias,
			declaresCallers: true,
			want:            undeclared, wantRef: ".context.uses.db.render_facts.pod_template_labels", field: "artifacts[probe.yaml]"},
		{name: "a pipeline step input reading a target's facts, undeclared", block: "pipeline-with",
			text: byAlias, want: inAStep, wantRef: ".context.uses.db.render_facts.pod_template_labels",
			field: "pipeline[patch].with"},
		{name: "a pipeline step gate reading each target's facts, undeclared", block: "pipeline-when",
			text: inRange, want: inAStep, wantRef: ".render_facts", field: "pipeline[patch].when"},
		{name: "a pipeline step input reading a target's facts under a FeatureRecipe that declares it",
			block: "pipeline-with", text: byAlias, declaresTargets: true,
			want: inAStep, wantRef: ".context.uses.db.render_facts.pod_template_labels", field: "pipeline[patch].with"},
		{name: "a pipeline step gate reading one target's facts under a FeatureRecipe that declares it",
			block: "pipeline-when", text: byIndex, declaresTargets: true,
			want: inAStep, wantRef: ".render_facts.container_ports", field: "pipeline[patch].when"},
		{name: "the same reading beneath an alias under a FeatureRecipe that declares it", block: "artifacts",
			text: byAlias, declaresTargets: true},
		{name: "the same reading in a range under a FeatureRecipe that declares it", block: "valuesMapping",
			text: inRange, declaresTargets: true},
		{name: "the same reading through with under a FeatureRecipe that declares both", block: "workload",
			text: byWith, declaresTargets: true, declaresCallers: true},
		{name: "a template reading the connections and not their facts", block: "artifacts",
			text: `{{ range .context.uses }}{{ .target }}{{ .provides.host }}{{ end }}`},
		{name: "a template naming a connection whose alias is the field's own name", block: "artifacts",
			text: `{{ .context.uses.render_facts.provides.host }}`},
		{name: "a template reading a render_facts field that is not beneath the connections", block: "artifacts",
			text: `{{ range .values.items }}{{ .render_facts }}{{ end }}`},
		{name: "a pipeline step input reading the host's own facts in full", block: "pipeline-with",
			text: hostOwn},
		{name: "a template reading its callers' facts is the other pair's subject", block: "artifacts",
			text: callersFacts},
		{name: "a step reading its callers' facts is the other pair's subject", block: "pipeline-with",
			text: callersFacts},
		{name: "a FeatureRecipe rendering once for a boundary is left to its own surface's rules",
			block: "artifacts", text: byAlias, scope: executionScopeBoundary},
		{name: "a step of a FeatureRecipe rendering once for a boundary is left to its own surface's rules",
			block: "pipeline-with", text: byAlias, scope: executionScopeBoundary},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fr := targetFactsFR(tc.block, tc.text, tc.declaresTargets, tc.declaresCallers)
			fr.Spec.ExecutionScope = tc.scope
			var factory EngineFactory
			if tc.pair != ([2]string{}) {
				factory = func(capability string) (Provider, error) {
					if capability != fr.Spec.Capability {
						return nil, errors.New("no provider for that capability")
					}
					return &stubArtifactsEmitter{left: tc.pair[0], right: tc.pair[1]}, nil
				}
			}
			halt := targetFactsHalt(t, fr, factory)

			if tc.want == nil {
				if halt != nil {
					t.Fatalf("the rule refused a template it admits: %v", halt)
				}
				return
			}
			if halt == nil {
				t.Fatalf("the rule admitted %s reading a target's render facts", tc.field)
			}
			if !errors.Is(halt, tc.want()) {
				t.Fatalf("the refusal does not carry the condition this field owes: %v", halt)
			}
			for _, other := range others {
				if errors.Is(tc.want(), other()) {
					continue
				}
				if errors.Is(halt, other()) {
					t.Errorf("the refusal also carries %v, so one fault has two conditions: %v", other(), halt)
				}
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

// TestTargetRenderFactsReference_ReadsTheShapesATemplateWrites holds the matcher on
// its own, over the reference sets the census produces, so a change to what counts
// as a reading shows as a row rather than as a rule that went quiet.
func TestTargetRenderFactsReference_ReadsTheShapesATemplateWrites(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		refs    []string
		wantRef string // "" where no reading is reported
	}{
		{name: "beneath one alias", refs: []string{".context.uses.db.render_facts"},
			wantRef: ".context.uses.db.render_facts"},
		{name: "a named fact beneath one alias", refs: []string{".context.uses.db.render_facts.container_ports"},
			wantRef: ".context.uses.db.render_facts.container_ports"},
		{name: "relative to an element of the map", refs: []string{".context.uses", ".render_facts.pod_template_labels"},
			wantRef: ".render_facts.pod_template_labels"},
		{name: "relative, with the map read through one alias", refs: []string{".context.uses.db", ".render_facts"},
			wantRef: ".render_facts"},
		{name: "relative, with no read of the map", refs: []string{".render_facts"}},
		{name: "a field of a connection that is not its facts", refs: []string{".context.uses.db.provides.host"}},
		{name: "a connection whose alias is the field's name", refs: []string{".context.uses.render_facts"}},
		{name: "a sibling field merely sharing the prefix", refs: []string{".context.uses.db.render_facts_extra"}},
		{name: "the callers' list is not the map", refs: []string{".context.used_by", ".render_facts"}},
		{name: "the host's own facts in full", refs: []string{".context.uses", ".context.render_facts"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ref, reads := targetRenderFactsReference(tc.refs)
			if tc.wantRef == "" {
				if reads {
					t.Fatalf("reported %q as reading a target's facts", ref)
				}
				return
			}
			if !reads || ref != tc.wantRef {
				t.Fatalf("got (%q, %v), want (%q, true)", ref, reads, tc.wantRef)
			}
		})
	}
}
