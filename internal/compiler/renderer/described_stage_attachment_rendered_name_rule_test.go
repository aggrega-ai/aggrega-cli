// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The proofs in this file cover the rule declared over the inline template fields
// the FeatureRecipe a PER-COMPONENT attachment binds writes.
//
// Every proof drives the PRODUCTION invocation site over the real presentation and
// the real enumeration, and each asserts against the refusal's own identity rather
// than against "an error". A rule declared and reached by nothing raises nothing,
// which is indistinguishable from a rule that ran and approved — so the calibration
// below measures the subject count before any verdict is read, and each refusal
// carries a control showing the outcome follows from the planted reference rather
// than from the fixture.
//
// The rule reads the HOST's reported name: an attachment's render context is
// derived from its host node's, so the value standing at the key is the host's
// answer, and the reference is refused exactly where that answer is absent. A
// control drives the reporting world and asserts the reference is admitted there,
// because a rule refusing whatever the host reports would refuse the one value an
// attachment needs to name its resources after its host.

const (
	attachmentRenderedNameNode = "my_db"
	attachmentRenderedNameName = "tls"
	attachmentRenderedNameType = "core/tls"
)

// attachmentRenderedNameFR is the FeatureRecipe under test: one authored template
// field written into the block the caller names.
//
// The field is selected by its block rather than fixed, because the family this
// covers is the whole set of template-bearing fields on a FeatureRecipe and a proof
// driving only one of them would say nothing about the others.
func attachmentRenderedNameFR(capability, block, text string) *types.FeatureRecipe {
	fr := &types.FeatureRecipe{}
	fr.Metadata.Name = attachmentRenderedNameName
	fr.Metadata.Version = "1.0.0"
	fr.Spec.Capability = capability
	switch block {
	case "valuesMapping":
		fr.Spec.ValuesMapping.Inline = text
	case "workload":
		fr.Spec.Workload.Inline = text
	case "artifacts":
		artifacts := types.OrderedMap[types.ArtifactEntry]{}
		artifacts.Set("probe.yaml", types.ArtifactEntry{Inline: text})
		fr.Spec.Artifacts = artifacts
	case "pipeline-with":
		fr.Spec.Pipeline = []types.PipelineStep{{ID: "patch", With: text}}
	case "pipeline-when":
		fr.Spec.Pipeline = []types.PipelineStep{{ID: "patch", When: text}}
	default:
		panic("attachmentRenderedNameFR: unknown block " + block)
	}
	return fr
}

// declaredAttachmentRenderedNameHalt drives the rendered-name rules through the
// production invocation site over one node carrying one attachment, with the
// reported-name collection the caller supplies, and returns the halt.
//
// The calibration walks the axis the TRAVERSAL walks rather than the inner accessor
// alone: an inner accessor answering correctly for a coordinate the outer axis never
// yields produces no subject at all, and a walk over no subject raises nothing —
// which reads exactly like a walk that ran and approved.
//
// The node's own Recipe is deliberately left carrying no template field, so any
// refusal the verdict shows is attributable to the attachment's document rather
// than to the host's.
func declaredAttachmentRenderedNameHalt(
	t *testing.T, fr *types.FeatureRecipe, names NodeRenderedNames,
) error {
	t.Helper()

	graph := &types.IRGraph{}
	graph.Spec.Nodes.Set(attachmentRenderedNameNode, types.IRNode{})
	entries := types.OrderedMap[types.IRFeatureEntry]{}
	entries.Set(attachmentRenderedNameName, types.IRFeatureEntry{Type: attachmentRenderedNameType})
	graph.Spec.FeatureValuesMapping.Set(attachmentRenderedNameNode, entries)

	hostRecipe := &types.Recipe{}
	hostRecipe.Metadata.Name = "store-recipe"
	hostRecipe.Metadata.Version = "1.0.0"
	hostRecipe.Spec.Capability = "helm"
	recipes := map[string]*types.Recipe{attachmentRenderedNameNode: hostRecipe}

	lookup := RecipeLookup{
		FeatureRecipe: func(instanceName, featureType string) (*types.FeatureRecipe, bool) {
			if instanceName == attachmentRenderedNameName && featureType == attachmentRenderedNameType {
				return fr, true
			}
			return nil, false
		},
	}

	view := newDescribedView(
		context.Background(), graph, stubGraphFacts{},
		recipes, lookup, nil, newStepDescriptorMemo(), nil, nil,
		names, nil, nil, nil,
	)
	if view == nil {
		t.Fatal("the view was not built; there is nothing for the rules to read")
	}
	axes := newDescribedStageCoordinates(
		graph,
		localsAxis{
			nodes:       []string{attachmentRenderedNameNode},
			attachments: []string{attachmentRenderedNameName},
		},
		recipes, lookup, nil,
	)
	if axes == nil {
		t.Fatal("the enumeration refused to build over a graph and a bound Recipe")
	}

	subjects := axes.AttachmentTemplateFields(attachmentRenderedNameNode, attachmentRenderedNameName)
	if want := len(AttachmentTemplateFields(fr)); len(subjects) != want {
		t.Fatalf("the axis presents %d subject(s) for a FeatureRecipe writing %d; the verdict below "+
			"would certify nothing", len(subjects), want)
	}
	if len(subjects) == 0 {
		t.Fatal("the axis presents no subject; the verdict below would certify nothing")
	}

	return runRenderedNameRule(context.Background(), view, axes)
}

// TestTheAttachmentRenderedNameRuleIsReachedAndItsObjectionsHalt is the invocation
// proof: one planted reference per fixture, driven through the production site,
// asserted against the refusal's own identity and against what the finding names.
//
// Every field family is driven, because the family is what is being proved: a proof
// driving one block would certify one field rather than the enumeration, and it is
// the enumeration that makes coverage assertable here.
func TestTheAttachmentRenderedNameRuleIsReachedAndItsObjectionsHalt(t *testing.T) {
	t.Parallel()

	// The host reports NO name, which is the world the refusal belongs to; the
	// control below drives the reporting world, where the reference is admitted.
	noNames := NodeRenderedNames{}

	for _, tc := range []struct {
		name  string
		block string
		text  string
		field string
	}{
		{
			name:  "an attachment's values mapping reading the name",
			block: "valuesMapping",
			text:  "host: \"{{ .context.rendered_name }}.svc\"",
			field: "valuesMapping",
		},
		{
			name:  "an attachment's inline workload reading the name",
			block: "workload",
			text:  "name: \"{{ .context.rendered_name }}\"",
			field: "workload",
		},
		{
			name:  "an attachment's inline artifacts entry reading the name",
			block: "artifacts",
			text:  "ref: \"{{ .context.rendered_name }}.svc\"",
			field: "artifacts[probe.yaml]",
		},
		{
			name:  "an attachment's pipeline step input reading the name",
			block: "pipeline-with",
			text:  "target: \"{{ .context.rendered_name }}\"",
			field: "pipeline[patch].with",
		},
		{
			// The gate is the quieter half of the same pair: a reference resolving
			// away there renders an empty string, which is falsy, so the step is
			// skipped as a pass-through and the build reports nothing at all.
			name:  "an attachment's pipeline step gate reading the name",
			block: "pipeline-when",
			text:  "{{ .context.rendered_name }}",
			field: "pipeline[patch].when",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fr := attachmentRenderedNameFR("helm", tc.block, tc.text)
			halt := declaredAttachmentRenderedNameHalt(t, fr, noNames)
			if halt == nil {
				t.Fatalf("the rules admitted %s reading a name its host's provider does not report", tc.field)
			}
			if !errors.Is(halt, ErrTemplateUnknownRoot()) {
				t.Fatalf("the refusal does not carry %v: %v", ErrTemplateUnknownRoot(), halt)
			}
			if !strings.Contains(halt.Error(), ".context.rendered_name") {
				t.Errorf("the refusal does not name the offending expression: %v", halt)
			}
			if !strings.Contains(halt.Error(), tc.field) {
				t.Errorf("the refusal does not name %q, so an author whose position stops at the "+
					"block cannot tell which field offends: %v", tc.field, halt)
			}
			if !strings.Contains(halt.Error(), "instance_id") {
				t.Errorf("the refusal does not spell the set this surface DOES define, so an author "+
					"is told what is refused and not what is available: %v", halt)
			}
			t.Logf("REFUSED %s -> %v", tc.name, halt)
		})
	}

	// 🔴 THE CONTROL THAT KEEPS THE REFUSAL SCOPED. Where the host's provider reports
	// a name, that name is the value an attachment names its resources after, so
	// every field family reading it is admitted. A rule refusing whatever the host
	// reports would refuse every row below.
	t.Run("the reference is admitted where the host's provider reports a name", func(t *testing.T) {
		t.Parallel()
		reported := NodeRenderedNames{attachmentRenderedNameNode: "my-zudb"}
		for _, block := range []string{
			"valuesMapping", "workload", "artifacts", "pipeline-with", "pipeline-when",
		} {
			fr := attachmentRenderedNameFR("helm", block, "{{ .context.rendered_name }}.svc")
			if halt := declaredAttachmentRenderedNameHalt(t, fr, reported); halt != nil {
				t.Errorf("the rules refused %s on a host whose provider reports a name, which is the "+
					"host's valid name and the value an attachment names its resources after: %v",
					block, halt)
			}
		}
	})
	// The same host-reports-a-name world with an attachment rendering through a
	// DIFFERENT capability from its host: the value is still the host's name, which
	// is what naming a resource after the host means, so it is admitted.
	t.Run("an attachment on another capability still reads its host's name", func(t *testing.T) {
		t.Parallel()
		reported := NodeRenderedNames{attachmentRenderedNameNode: "my-zudb"}
		fr := attachmentRenderedNameFR("compose", "artifacts", "{{ .context.rendered_name }}.svc")
		if halt := declaredAttachmentRenderedNameHalt(t, fr, reported); halt != nil {
			t.Errorf("the rules refused the host's reported name to an attachment declaring another "+
				"capability: %v", halt)
		}
	})

	// The second control: a field reading the IDENTITY is admitted, so the refusal
	// watches one key rather than the identity pillar. `.context.instance_id` on an
	// attachment's surface names the HOST COMPONENT the attachment is resolving for,
	// which is what it names on every other surface, and that is a value this
	// surface does supply.
	t.Run("an attachment field reading the identity is admitted", func(t *testing.T) {
		t.Parallel()
		for _, block := range []string{
			"valuesMapping", "workload", "artifacts", "pipeline-with", "pipeline-when",
		} {
			fr := attachmentRenderedNameFR("helm", block, "{{ .context.instance_id }}.svc")
			if halt := declaredAttachmentRenderedNameHalt(t, fr, NodeRenderedNames{}); halt != nil {
				t.Errorf("the rules refused %s whose only reference is the identity, which this "+
					"surface does supply: %v", block, halt)
			}
		}
	})

	// The third control: a BOUNDARY-scope attachment ends the rule. Such an
	// attachment renders once for a boundary against a context whose identity pillar
	// is excluded whole, so the reference is already refused by the rules declared
	// over that surface — and reporting it here as well would give one authoring
	// fault two positions.
	t.Run("a boundary-scope attachment is left to its own surface's rules", func(t *testing.T) {
		t.Parallel()
		for _, block := range []string{
			"valuesMapping", "workload", "artifacts", "pipeline-with", "pipeline-when",
		} {
			fr := attachmentRenderedNameFR("helm", block, "{{ .context.rendered_name }}.svc")
			fr.Spec.ExecutionScope = executionScopeBoundary
			if halt := declaredAttachmentRenderedNameHalt(t, fr, NodeRenderedNames{}); halt != nil {
				t.Errorf("the rules reported %s on a boundary-scope attachment, which the rules over "+
					"that surface already refuse: %v", block, halt)
			}
		}
	})
}

// TestTheAttachmentTemplateFieldEnumerationLeavesProvidesAndLocalsToTheirOwnAxes
// pins the set difference rather than a count, because both sides short by one
// still agree on a count.
//
// The FeatureRecipe's provides and locals entries are refused directly by the scope
// their own evaluation is checked against, so enumerating them here as well would
// report one authoring fault against two subjects and give a reader two positions
// for one edit. Its inject mapping is absent for a different reason: it is
// evaluated against the consuming node's delivery pipe rather than against this
// context.
func TestTheAttachmentTemplateFieldEnumerationLeavesProvidesAndLocalsToTheirOwnAxes(t *testing.T) {
	t.Parallel()

	fr := &types.FeatureRecipe{}
	fr.Metadata.Name = attachmentRenderedNameName
	fr.Spec.Capability = "helm"
	fr.Spec.ValuesMapping.Inline = "a: 1"
	fr.Spec.Workload.Inline = "b: 2"
	artifacts := types.OrderedMap[types.ArtifactEntry]{}
	artifacts.Set("probe.yaml", types.ArtifactEntry{Inline: "c: 3"})
	fr.Spec.Artifacts = artifacts
	fr.Spec.Pipeline = []types.PipelineStep{{ID: "patch", With: "d: 4", When: "{{ true }}"}}
	fr.Spec.Provides = omLocals("host", "{{ .context.instance_id }}")
	fr.Spec.Locals = omLocals("name", "{{ .attachment_name }}")
	fr.Spec.InjectMapping = "env: {}"

	var labels []string
	for _, field := range AttachmentTemplateFields(fr) {
		labels = append(labels, field.Label)
	}

	want := []string{
		"valuesMapping", "workload", "artifacts[probe.yaml]",
		"pipeline[patch].with", "pipeline[patch].when",
	}
	for _, w := range want {
		if !slices.Contains(labels, w) {
			t.Errorf("the enumeration omits %q, so no rule over this axis is invoked for it and "+
				"nothing reports the omission; it carries %v", w, labels)
		}
	}
	for _, unwanted := range []string{"provides[host]", "locals[name]", "injectMapping"} {
		if slices.Contains(labels, unwanted) {
			t.Errorf("the enumeration carries %q, which has its own refusal at this stage — so one "+
				"authoring fault would be reported against two subjects; it carries %v",
				unwanted, labels)
		}
	}
	if len(labels) != len(want) {
		t.Errorf("the enumeration carries %d field(s) where %d are declared over this context; "+
			"it carries %v", len(labels), len(want), labels)
	}
}

// TestTheAttachmentAndRecipeTemplateFieldEnumerationsShareOneBody pins that the two
// enumerations agree on identically-shaped input, which is what makes them two
// readings of one body rather than two bodies that can come to disagree.
//
// It compares the LABELS the two produce for the same four blocks. A divergence
// here is the silent kind: a block one of them stopped enumerating would be a
// surface no rule was invoked over, which reports exactly what a rule that ran and
// approved reports.
func TestTheAttachmentAndRecipeTemplateFieldEnumerationsShareOneBody(t *testing.T) {
	t.Parallel()

	artifacts := types.OrderedMap[types.ArtifactEntry]{}
	artifacts.Set("probe.yaml", types.ArtifactEntry{Inline: "c: 3"})
	pipeline := []types.PipelineStep{{ID: "patch", With: "d: 4", When: "{{ true }}"}}

	recipe := &types.Recipe{}
	recipe.Spec.ValuesMapping.Inline = "a: 1"
	recipe.Spec.Workload.Inline = "b: 2"
	recipe.Spec.Artifacts = artifacts
	recipe.Spec.Pipeline = pipeline

	fr := &types.FeatureRecipe{}
	fr.Spec.ValuesMapping.Inline = "a: 1"
	fr.Spec.Workload.Inline = "b: 2"
	fr.Spec.Artifacts = artifacts
	fr.Spec.Pipeline = pipeline

	fromRecipe := RecipeTemplateFields(recipe)
	fromFR := AttachmentTemplateFields(fr)

	if len(fromRecipe) != len(fromFR) {
		t.Fatalf("the two enumerations carry %d and %d field(s) for identically-shaped blocks",
			len(fromRecipe), len(fromFR))
	}
	for i := range fromRecipe {
		if fromRecipe[i].Label != fromFR[i].Label {
			t.Errorf("field %d: the Recipe reading labels it %q and the FeatureRecipe reading %q",
				i, fromRecipe[i].Label, fromFR[i].Label)
		}
		if !slices.Equal(fromRecipe[i].AuthoredKeys, fromFR[i].AuthoredKeys) {
			t.Errorf("field %d (%s): the two readings locate it at %v and %v, so a finding on one "+
				"surface would be positioned differently from the identical finding on the other",
				i, fromRecipe[i].Label, fromRecipe[i].AuthoredKeys, fromFR[i].AuthoredKeys)
		}
	}
	if len(fromRecipe) == 0 {
		t.Fatal("both enumerations are empty, so the comparison above certifies nothing")
	}
}
