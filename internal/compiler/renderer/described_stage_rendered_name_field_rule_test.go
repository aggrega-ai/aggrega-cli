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

// The proofs in this file cover the rule declared over the inline template fields
// a node's own Recipe writes, which is the half of the rendered-name refusal that
// does not run over a provides entry.
//
// Every proof drives the PRODUCTION invocation site over the real presentation and
// the real enumeration, and each asserts against the halt's own identity rather
// than against "an error". A rule declared and reached by nothing raises nothing,
// which is indistinguishable from a rule that ran and approved — so the
// calibration below measures the subject count before any verdict is read, and the
// admitted control beside each refusal shows the halt is a consequence of the
// planted reference rather than of the fixture.
//
// Two of the field families this rule reaches cannot be driven through a real
// build at all: a pipeline step's input and its gate need a resolvable
// pipeline-step plugin in the corpus, and the refusal fires before any plugin is
// invoked, so a build authored to exercise them fails earlier on the reference to
// a plugin the corpus does not carry. Those two families are covered here and
// nowhere else, which is why the rows below walk every family rather than one.

const renderedNameFieldNode = "my_db"

// renderedNameFieldRecipe is the Recipe under test: one authored template field
// written into the block the caller names, under the capability the caller states.
//
// The field is selected by its block rather than fixed, because the family this
// covers is the whole set of template-bearing fields on a Recipe and a proof
// driving only one of them would say nothing about the others.
func renderedNameFieldRecipe(capability, block, text string) *types.Recipe {
	r := &types.Recipe{}
	r.Metadata.Name = "store-recipe"
	r.Metadata.Version = "1.0.0"
	r.Spec.Capability = capability
	switch block {
	case "valuesMapping":
		r.Spec.ValuesMapping.Inline = text
	case "workload":
		r.Spec.Workload.Inline = text
	case "artifacts":
		artifacts := types.OrderedMap[types.ArtifactEntry]{}
		artifacts.Set("probe.yaml", types.ArtifactEntry{Inline: text})
		r.Spec.Artifacts = artifacts
	case "pipeline-with":
		r.Spec.Pipeline = []types.PipelineStep{{ID: "patch", With: text}}
	case "pipeline-when":
		r.Spec.Pipeline = []types.PipelineStep{{ID: "patch", When: text}}
	case "provides":
		r.Spec.Provides = omLocals("host", text)
	default:
		panic("renderedNameFieldRecipe: unknown block " + block)
	}
	return r
}

// declaredRenderedNameHalt drives the rendered-name rules through the production
// invocation site over one authored Recipe, with the reported-name collection the
// caller supplies, and returns the halt.
//
// The calibration is taken before the verdict is read, and it walks the axis the
// TRAVERSAL walks rather than the inner accessor alone: an inner accessor
// answering correctly for a coordinate the outer axis never yields produces no
// subject at all, and a walk over no subject raises nothing — which reads exactly
// like a walk that ran and approved.
func declaredRenderedNameHalt(t *testing.T, recipe *types.Recipe, names NodeRenderedNames) error {
	t.Helper()
	graph := &types.IRGraph{}
	graph.Spec.Nodes.Set(renderedNameFieldNode, types.IRNode{})
	recipes := map[string]*types.Recipe{renderedNameFieldNode: recipe}
	lookup := RecipeLookup{}

	view := newDescribedView(
		context.Background(), graph, stubGraphFacts{},
		recipes, lookup, nil, newStepDescriptorMemo(), nil, nil,
		names, nil,
	)
	if view == nil {
		t.Fatal("the presentation was not built; there is nothing for the rules to read")
	}
	axes := newDescribedStageCoordinates(
		graph, localsAxis{nodes: []string{renderedNameFieldNode}}, recipes, lookup, nil,
	)
	if axes == nil {
		t.Fatal("the enumeration refused to build over a graph and a bound Recipe")
	}

	subjects := len(axes.NodeTemplateFields(renderedNameFieldNode)) +
		len(axes.ProvidesKeys(renderedNameFieldNode))
	if want := len(RecipeTemplateFields(recipe)) + recipe.Spec.Provides.Len(); subjects != want {
		t.Fatalf("the axes present %d subject(s) for a Recipe writing %d; the verdict below would "+
			"certify nothing", subjects, want)
	}
	if subjects == 0 {
		t.Fatal("the axes present no subject; the verdict below would certify nothing")
	}

	return runRenderedNameRule(context.Background(), view, axes)
}

// TestTheRenderedNameFieldRuleIsReachedAndItsObjectionsHalt is the invocation
// proof: one planted reference per fixture, driven through the production site,
// asserted against the halt's own identity and against what the finding names.
//
// # Every field family is driven, because the family is what is being proved
//
// The rows walk each block a Recipe writes a template into, the provides entry
// included so the shared predicate is exercised from both of its declaring
// values. A proof driving one of them would certify one field rather than the
// enumeration, and it is the enumeration that makes coverage assertable here.
//
// # The field label is asserted separately from the position, and that is not
// belt-and-braces
//
// The locator dialect stops at an authored key containing its own separator, so
// an artifacts entry keyed by an output filename positions at the block and no
// further. Where the position stops short the detail is the only thing that says
// which field inside the block offends, and a rule that dropped it would report a
// real fault an author cannot locate.
func TestTheRenderedNameFieldRuleIsReachedAndItsObjectionsHalt(t *testing.T) {
	t.Parallel()

	// No node reports a name, which is the condition the refusal is about.
	noNames := NodeRenderedNames{}

	for _, tc := range []struct {
		name  string
		block string
		text  string
		field string
	}{
		{
			name:  "a values mapping reading the name",
			block: "valuesMapping",
			text:  "host: \"{{ .context.rendered_name }}.svc\"",
			field: "valuesMapping",
		},
		{
			name:  "an inline workload reading the name",
			block: "workload",
			text:  "name: \"{{ .context.rendered_name }}\"",
			field: "workload",
		},
		{
			name:  "an inline artifacts entry reading the name",
			block: "artifacts",
			text:  "ref: \"{{ .context.rendered_name }}.svc\"",
			field: "artifacts[probe.yaml]",
		},
		{
			// Not reachable through a real build: the refusal fires before any
			// pipeline plugin is invoked, so a corpus authored to exercise it fails
			// earlier on the plugin reference itself. This row is the only cover.
			name:  "a pipeline step's input reading the name",
			block: "pipeline-with",
			text:  "target: \"{{ .context.rendered_name }}\"",
			field: "pipeline[patch].with",
		},
		{
			// The gate is the quieter half of the same pair: a reference resolving
			// away there renders an empty string, which is falsy, so the step is
			// skipped as a pass-through and the build reports nothing at all.
			name:  "a pipeline step's gate reading the name",
			block: "pipeline-when",
			text:  "{{ .context.rendered_name }}",
			field: "pipeline[patch].when",
		},
		{
			name:  "a provides entry reading the name",
			block: "provides",
			text:  "{{ .context.rendered_name }}.svc.cluster.local",
			field: "host",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			recipe := renderedNameFieldRecipe("terraform", tc.block, tc.text)
			halt := declaredRenderedNameHalt(t, recipe, noNames)
			if halt == nil {
				t.Fatalf("the rules admitted %s reading a name no provider reports", tc.field)
			}
			if !errors.Is(halt, ErrRenderedNameUnavailable()) {
				t.Fatalf("the halt does not carry %v: %v", ErrRenderedNameUnavailable(), halt)
			}
			if !strings.Contains(halt.Error(), ".context.rendered_name") {
				t.Errorf("the halt does not name the offending expression: %v", halt)
			}
			if !strings.Contains(halt.Error(), tc.field) {
				t.Errorf("the halt does not name %q, so an author whose position stops at the block "+
					"cannot tell which field offends: %v", tc.field, halt)
			}
			if !strings.Contains(halt.Error(), "terraform") {
				t.Errorf("the halt does not name the provider that reports no such name: %v", halt)
			}
			t.Logf("HALT %s -> %v", tc.name, halt)
		})
	}

	// The first control: the same worlds with the node's provider REPORTING a name
	// are admitted, so every refusal above is attributable to the absent name and
	// not to the fixture.
	t.Run("every field is admitted once the provider reports a name", func(t *testing.T) {
		t.Parallel()
		reported := NodeRenderedNames{renderedNameFieldNode: "my-zudb"}
		for _, block := range []string{
			"valuesMapping", "workload", "artifacts", "pipeline-with", "pipeline-when", "provides",
		} {
			recipe := renderedNameFieldRecipe("helm", block, "{{ .context.rendered_name }}.svc")
			if halt := declaredRenderedNameHalt(t, recipe, reported); halt != nil {
				t.Errorf("the rules refused %s on a node whose provider reports a name, which is "+
					"exactly what that template is supposed to read: %v", block, halt)
			}
		}
	})

	// The second control: a field reading the IDENTITY rather than the reported
	// name is admitted on a node reporting none, so the refusal is watching one key
	// rather than the identity pillar.
	t.Run("a field reading the identity is admitted where no name is reported", func(t *testing.T) {
		t.Parallel()
		for _, block := range []string{
			"valuesMapping", "workload", "artifacts", "pipeline-with", "pipeline-when", "provides",
		} {
			recipe := renderedNameFieldRecipe("terraform", block, "{{ .context.instance_id }}.svc")
			if halt := declaredRenderedNameHalt(t, recipe, NodeRenderedNames{}); halt != nil {
				t.Errorf("the rules refused %s whose only reference is the identity, which is the "+
					"value that IS correct there: %v", block, halt)
			}
		}
	})
}

// TestTheRenderedNameRemedyFollowsTheNodeRatherThanTheSurface pins the property
// that makes the shared predicate correct on every surface it reaches: which
// remedy an occurrence carries is decided by whether the NODE supplies source,
// never by which field the reference sits in.
//
// A remedy keyed on the surface would tell an author on one field to read the
// identity and an author on another not to, for one node — and one of the two
// would be wrong, because whether the identity is the right substitute is a
// property of the node's own input rather than of where the reference was written.
func TestTheRenderedNameRemedyFollowsTheNodeRatherThanTheSurface(t *testing.T) {
	t.Parallel()

	sourceless := renderedNameRemedy(false)
	sourced := renderedNameRemedy(true)

	for _, block := range []string{"valuesMapping", "workload", "artifacts", "provides"} {
		bare := renderedNameFieldRecipe("terraform", block, "{{ .context.rendered_name }}")
		haltBare := declaredRenderedNameHalt(t, bare, NodeRenderedNames{})
		if haltBare == nil {
			t.Fatalf("%s: no halt, so there is no remedy to read", block)
		}
		if !strings.Contains(haltBare.Error(), sourceless) {
			t.Errorf("%s on a node supplying no source does not carry the remedy naming the identity: %v",
				block, haltBare)
		}

		supplying := renderedNameFieldRecipe("terraform", block, "{{ .context.rendered_name }}")
		supplying.Spec.Source = "./charts/pg"
		haltSourced := declaredRenderedNameHalt(t, supplying, NodeRenderedNames{})
		if haltSourced == nil {
			t.Fatalf("%s: no halt on the source-supplying node", block)
		}
		if !strings.Contains(haltSourced.Error(), sourced) {
			t.Errorf("%s on a node supplying source does not carry the no-substitute remedy: %v",
				block, haltSourced)
		}
	}
}

// TestTheRecipeTemplateFieldEnumerationCarriesEveryInlineBlock holds the shared
// enumeration to the field set it is the one home of.
//
// It is asserted as a SET DIFFERENCE naming what is missing rather than as a
// count, because two sides short by one still agree on a count. The enumeration is
// what the axis, the presentation and the delimiter reading all walk, so a block
// dropped from it goes unchecked on every one of them at once with nothing
// reporting the omission — which is the failure an enumerated axis exists to
// prevent.
//
// The two blocks it must NOT carry are asserted in the same table rather than
// separately: provides and locals each have their own axis at this stage, so
// enumerating them here would report one authoring fault against two subjects.
func TestTheRecipeTemplateFieldEnumerationCarriesEveryInlineBlock(t *testing.T) {
	t.Parallel()

	r := &types.Recipe{}
	r.Metadata.Name = "store-recipe"
	r.Spec.ValuesMapping.Inline = "{{ .values.a }}"
	r.Spec.Workload.Inline = "{{ .values.b }}"
	artifacts := types.OrderedMap[types.ArtifactEntry]{}
	artifacts.Set("probe.yaml", types.ArtifactEntry{Inline: "{{ .values.c }}"})
	artifacts.Set("second.json", types.ArtifactEntry{Inline: "{{ .values.d }}"})
	r.Spec.Artifacts = artifacts
	r.Spec.Pipeline = []types.PipelineStep{{ID: "patch", With: "{{ .values.e }}", When: "{{ .values.f }}"}}
	// Neither of these may be enumerated here.
	r.Spec.Provides = omLocals("host", "{{ .values.g }}")
	r.Spec.Locals = omLocals("fqdn", "{{ .values.h }}")

	// The label and the authored keys are both asserted, because they answer two
	// different questions and a correct label over wrong keys positions a finding at
	// a path the file does not contain.
	want := map[string][]string{
		"valuesMapping":          {"valuesMapping"},
		"workload":               {"workload"},
		"artifacts[probe.yaml]":  {"artifacts", "probe.yaml"},
		"artifacts[second.json]": {"artifacts", "second.json"},
		"pipeline[patch].with":   {"pipeline", "patch", "with"},
		"pipeline[patch].when":   {"pipeline", "patch", "when"},
	}

	got := map[string][]string{}
	for _, field := range RecipeTemplateFields(r) {
		got[field.Label] = field.AuthoredKeys
	}
	for label, keys := range want {
		haveKeys, present := got[label]
		if !present {
			t.Errorf("the enumeration omits %q, so a template written there is checked by nothing", label)
			continue
		}
		if strings.Join(haveKeys, ".") != strings.Join(keys, ".") {
			t.Errorf("%q is located at .spec.%s; want .spec.%s",
				label, strings.Join(haveKeys, "."), strings.Join(keys, "."))
		}
	}
	for label := range got {
		if _, expected := want[label]; !expected {
			t.Errorf("the enumeration carries %q, which this proof does not account for; add a row "+
				"rather than widening the assertion", label)
		}
	}

	// A field whose content is imported rather than written inline carries no
	// authored text, and the bound is asserted rather than left implied: such a
	// field is resolved when the render reaches it, so enumerating it here would
	// present a rule with nothing to judge.
	imported := &types.Recipe{}
	imported.Metadata.Name = "store-recipe"
	imported.Spec.ValuesMapping.Import = "./mapping.tmpl"
	importedArtifacts := types.OrderedMap[types.ArtifactEntry]{}
	importedArtifacts.Set("probe.yaml", types.ArtifactEntry{Import: "/abs/probe.yaml"})
	imported.Spec.Artifacts = importedArtifacts
	if fields := RecipeTemplateFields(imported); len(fields) != 0 {
		t.Errorf("a Recipe writing no inline template presents %d field(s); want none", len(fields))
	}
}

// TestTheArtifactDelimiterReadingFollowsTheLabelFamily holds the reading that
// decides which delimiter pair a field's text is parsed with.
//
// It matters in both directions and the second is the one a reader is likely to
// miss. Read with the usual pair, an artifacts body written for a provider
// declaring an alternate one would have that provider's own downstream syntax
// surface as references the author never made — refusing legal authoring. Read
// with the alternate pair, every other field would surface none of the references
// it does make — admitting exactly what the rule exists to refuse.
//
// The rows include an artifacts entry whose authored key opens with another
// family's word, because the label grammar joins fixed text this package writes to
// whatever the author typed, and only the fixed part may decide the family.
func TestTheArtifactDelimiterReadingFollowsTheLabelFamily(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		label    string
		artifact bool
	}{
		{label: "valuesMapping", artifact: false},
		{label: "workload", artifact: false},
		{label: "pipeline[patch].with", artifact: false},
		{label: "pipeline[patch].when", artifact: false},
		{label: "artifacts[probe.yaml]", artifact: true},
		{label: "artifacts[pipeline[x].with]", artifact: true},
		{label: "artifacts[workload]", artifact: true},
	} {
		if got := RecipeTemplateFieldReadsWithArtifactDelimiters(tc.label); got != tc.artifact {
			t.Errorf("%q reads with the artifact pair=%v, want %v", tc.label, got, tc.artifact)
		}
	}

	// The reading is held to the labels the enumeration actually builds, so a
	// change to the label grammar cannot leave the two disagreeing while both keep
	// compiling.
	r := &types.Recipe{}
	r.Metadata.Name = "store-recipe"
	r.Spec.ValuesMapping.Inline = "a"
	r.Spec.Workload.Inline = "b"
	artifacts := types.OrderedMap[types.ArtifactEntry]{}
	artifacts.Set("probe.yaml", types.ArtifactEntry{Inline: "c"})
	r.Spec.Artifacts = artifacts
	r.Spec.Pipeline = []types.PipelineStep{{ID: "patch", With: "d", When: "e"}}

	for _, field := range RecipeTemplateFields(r) {
		wantArtifact := len(field.AuthoredKeys) > 0 && field.AuthoredKeys[0] == recipeFieldArtifacts
		if got := RecipeTemplateFieldReadsWithArtifactDelimiters(field.Label); got != wantArtifact {
			t.Errorf("the enumeration built %q under .spec.%s, and the reading says artifact pair=%v",
				field.Label, strings.Join(field.AuthoredKeys, "."), got)
		}
	}
}
