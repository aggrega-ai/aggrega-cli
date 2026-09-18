// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The proofs in this file cover the rule declared over the source entry a node's
// own Recipe writes: that the production site reaches it, that the axis presents a
// position for a Recipe whether or not the author wrote anything at it, that the
// two authoring modes the entry selects between are both admitted, that a finding
// carries the authored position the declaration exists to add, and that every
// offending node is reported rather than the first.
//
// Two of them are load-bearing and neither is a formality.
//
// The ENUMERATION proof, because this kind is keyed by a POSITION rather than by
// anything the author named. An axis presenting the position only where something
// was written at it would be invoked no times for exactly the documents the rule
// exists to refuse, and a rule invoked zero times reports what a rule that ran and
// approved reports. The proof therefore measures the subject set over a Recipe that
// writes nothing at the position, which is the case an axis keyed on content loses.
//
// The POSITION proof, because the refusal this rule stands in front of names no
// document at all: it takes the Recipe's path, which the catalog loader does not
// retain on a parsed Recipe, with no fallback to the Recipe's name. What the
// declaration adds is therefore a document AND a key inside it, and an assertion
// that read only the sentence would report the two as equivalent.

// recipeSourceProbe is the authored document the rows below are driven with: one
// Recipe whose source entry carries whatever the row is about.
//
// It is built through the same helper every other Recipe-shaped proof in this
// package uses, so the rows differ in the source entry and in nothing else.
func recipeSourceProbe(source any) *types.Recipe {
	r := minimalRecipe("source-probe")
	r.Spec.Source = source
	return r
}

// recipeSourceProbeWithoutEntry is the Recipe that writes no source entry at all —
// the sourceless authoring mode, which the rule must admit and which the axis must
// still present a position for.
func recipeSourceProbeWithoutEntry() *types.Recipe {
	r := minimalRecipe("source-probe")
	r.Spec.Source = nil
	return r
}

// declaredRecipeSourceHalt drives the declared rule through the PRODUCTION
// invocation site over one authored Recipe, with the real view and the real
// enumeration, and returns the halt.
//
// Both sides are production code: the view is the one the render builds and the
// axis is the one the render derives, so the verdict is the declared route's rather
// than a second reading of the Recipe assembled here.
//
// The subject count is measured before the verdict is taken, because a verdict from
// a walk that reached no subject reports exactly what an admitted Recipe reports.
func declaredRecipeSourceHalt(t *testing.T, recipe *types.Recipe) error {
	t.Helper()
	const node = "api"
	recipes := map[string]*types.Recipe{node: recipe}

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(node, types.IRNode{})
	graph := &types.IRGraph{Spec: types.IRSpec{Nodes: nodes}}

	view := newDescribedView(
		context.Background(), graph, stubGraphFacts{},
		recipes, RecipeLookup{}, nil, newStepDescriptorMemo(), nil, nil,
		nil, nil, nil, nil,
	)
	if view == nil {
		t.Fatal("the view was not built; there is nothing for the rule to read")
	}
	axes := newDescribedStageCoordinates(graph, localsAxis{nodes: []string{node}}, recipes, RecipeLookup{}, nil)
	if axes == nil {
		t.Fatal("the enumeration refused to build over a graph and a bound Recipe")
	}
	if got := len(axes.RecipeSourceFields(node)); got != 1 {
		t.Fatalf("the axis presents %d source position(s) for a bound Recipe; exactly one exists per "+
			"Recipe, so the verdict below would certify nothing", got)
	}
	return runRecipeSourceRule(context.Background(), view, axes)
}

// resolvedRecipeSourceHalt drives the SAME authored Recipe through the render
// sub-pass the declared rule stands ahead of, and returns its halt.
//
// It is the production sub-pass rather than a direct call on the predicate: what
// the rule has to be compared with is what a build does at the point the source is
// read as the author wrote it.
func resolvedRecipeSourceHalt(recipe *types.Recipe) error {
	state := NewPhaseBState(recipe, "api", "default", "")
	return phaseB0(minimalCtx(), state, types.OrderedMap[string]{})
}

// TestRecipeSourceRule_PresentsThePositionWhateverWasWrittenAtIt is the enumeration
// proof, and it is the one assertion the rest of this file rests on.
//
// A source entry is not a set the document decides: exactly one position exists per
// bound Recipe. So the axis is measured over a Recipe that writes the entry, one
// that writes nothing at it, and a node with no bound Recipe at all — the three
// states an author can put the compiler in — because an axis that presented the
// position only for the first would leave the rule uninvoked for precisely the
// documents it exists to refuse.
func TestRecipeSourceRule_PresentsThePositionWhateverWasWrittenAtIt(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		recipe *types.Recipe
		want   int
	}{
		{name: "an entry naming a real source", recipe: recipeSourceProbe("./chart"), want: 1},
		{name: "an entry written and left empty", recipe: recipeSourceProbe(""), want: 1},
		{name: "no entry written at all", recipe: recipeSourceProbeWithoutEntry(), want: 1},
		{name: "no bound Recipe", recipe: nil, want: 0},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			const node = "api"
			recipes := map[string]*types.Recipe{}
			if tc.recipe != nil {
				recipes[node] = tc.recipe
			}
			nodes := types.OrderedMap[types.IRNode]{}
			nodes.Set(node, types.IRNode{})
			graph := &types.IRGraph{Spec: types.IRSpec{Nodes: nodes}}

			axes := newDescribedStageCoordinates(
				graph, localsAxis{nodes: []string{node}}, recipes, RecipeLookup{}, nil)
			if axes == nil {
				t.Fatal("the enumeration refused to build over a graph")
			}
			fields := axes.RecipeSourceFields(node)
			if len(fields) != tc.want {
				t.Fatalf("the axis presented %d position(s); want %d", len(fields), tc.want)
			}
			if tc.want == 0 {
				return
			}
			if fields[0].Value != "source" {
				t.Errorf("the axis presented coordinate %q; the position is the authoring surface's "+
					"own spec-root key", fields[0].Value)
			}
			// The coordinate is positioned at the Recipe, which is the document an
			// author edits to change the entry.
			if fields[0].SourceFile != tc.recipe.Metadata.Name {
				t.Errorf("the position is attributed to %q; want the Recipe %q",
					fields[0].SourceFile, tc.recipe.Metadata.Name)
			}
			// The inline template-field axis presents no such field, so the two are
			// disjoint rather than two readings of one set.
			for _, inline := range axes.NodeTemplateFields(node) {
				if inline.Value == "source" {
					t.Error("the inline template-field axis presented the source entry, so the two " +
						"axes would answer for one position twice")
				}
			}
			// And the import axis does not carry it either, which is the widening the
			// sibling axis refuses in its own contract.
			for _, imported := range axes.RecipeImportFields(node) {
				if imported.Value == "source" {
					t.Error("the import-field axis presented the source entry; that axis reports which " +
						"fields carry an import, and a source entry carries a path")
				}
			}
		})
	}
}

// TestRecipeSourceRule_AdmitsTheTwoDeclaredModes is the rule's boundary: the modes
// the empty entry is ambiguous BETWEEN must both survive it.
//
// Without these rows the refusal proved below is satisfied by a rule that refuses
// every Recipe, and the axis's own property — that it presents a position for a
// document that wrote nothing at it — would be what made such a rule look correct.
func TestRecipeSourceRule_AdmitsTheTwoDeclaredModes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		recipe *types.Recipe
	}{
		{name: "no entry at all, which is sourceless mode", recipe: recipeSourceProbeWithoutEntry()},
		{name: "an entry naming a path, which is sourced mode", recipe: recipeSourceProbe("./chart")},
		{
			name:   "an entry naming a structured source",
			recipe: recipeSourceProbe(map[string]any{"kind": "helm-oci-chart", "url": "oci://example.test/c"}),
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := declaredRecipeSourceHalt(t, tc.recipe); err != nil {
				t.Errorf("a declared authoring mode was refused: %v", err)
			}
		})
	}
}

// TestRecipeSourceRule_CarryTheAuthoredPosition is the comparison with the refusal
// the rule stands ahead of, and the position is where the two differ.
//
// The refusal at the point the source is read takes the Recipe's path with no
// fallback, and the catalog loader does not retain a path on a parsed Recipe — so on
// the production walk it names no document at all. The declaration names the Recipe
// and the key inside it, which is the whole of what it adds.
func TestRecipeSourceRule_CarryTheAuthoredPosition(t *testing.T) {
	t.Parallel()

	recipe := recipeSourceProbe("")

	declared := declaredRecipeSourceHalt(t, recipe)
	if declared == nil {
		t.Fatal("the declared rule admitted an entry written and left empty")
	}
	resolved := resolvedRecipeSourceHalt(recipe)
	if resolved == nil {
		t.Fatal("the refusal at the point the source is read admitted an empty entry")
	}

	before, after := resolved.Error(), declared.Error()
	t.Logf("before (the refusal at the point the source is read): %s", before)
	t.Logf("after  (the declared rule):                           %s", after)

	if strings.Contains(before, " at .") {
		t.Errorf("the earlier refusal already carries an authored locator, so the declaration adds "+
			"none: %s", before)
	}
	if !strings.Contains(after, "source-probe at .spec.source") {
		t.Errorf("the declared finding does not carry the framework-supplied position: %s", after)
	}
	// The remedy is what an author acts on and it is a property of the occurrence
	// rather than of the condition, so it is carried as advice on both sides.
	for _, want := range []string{"omit the source field", "sourceless mode", "sourced mode"} {
		if !strings.Contains(after, want) {
			t.Errorf("the declared finding does not name %q, so the two modes it is ambiguous between "+
				"are not both stated: %s", want, after)
		}
	}
	// And the identity, read off the chain rather than off the rendering: a
	// single-line rendering is the category, the position and the sentence, and no
	// condition's short code appears in it.
	if code, ok := cerrors.ShortCodeOf(declared); !ok || code != "E0251" {
		t.Errorf("the finding carries short code %q (present=%v); want E0251 — the condition the "+
			"declaration registers is what a formatter names the diagnostic by", code, ok)
	}
}

// TestRecipeSourceRule_ReportEveryOffendingNode is the breadth of the walk: two
// Recipes each leaving the entry empty draw two findings, each at its own position.
//
// The breadth is read from the diagnostics beneath the halt rather than from the
// envelope's own line, because that line is the category and the wording this rule
// supplied and never the findings underneath — an assertion over it would report a
// rule that found nothing exactly as it reports a rule that found everything.
func TestRecipeSourceRule_ReportEveryOffendingNode(t *testing.T) {
	t.Parallel()

	first := recipeSourceProbe("")
	first.Metadata.Name = "first-recipe"
	second := recipeSourceProbe("")
	second.Metadata.Name = "second-recipe"

	recipes := map[string]*types.Recipe{"api": first, "web": second}
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{})
	nodes.Set("web", types.IRNode{})
	graph := &types.IRGraph{Spec: types.IRSpec{Nodes: nodes}}

	view := newDescribedView(
		context.Background(), graph, stubGraphFacts{},
		recipes, RecipeLookup{}, nil, newStepDescriptorMemo(), nil, nil,
		nil, nil, nil, nil,
	)
	if view == nil {
		t.Fatal("the view was not built")
	}
	axes := newDescribedStageCoordinates(
		graph, localsAxis{nodes: []string{"api", "web"}}, recipes, RecipeLookup{}, nil)
	if axes == nil {
		t.Fatal("the enumeration refused to build")
	}

	err := runRecipeSourceRule(context.Background(), view, axes)
	if err == nil {
		t.Fatal("two Recipes leaving the source entry empty were both admitted")
	}
	t.Logf("the envelope: %s", err.Error())
	leaves := haltLeafMessages(err)
	for i, leaf := range leaves {
		t.Logf("finding %d: %s", i+1, leaf)
	}
	if len(leaves) != 2 {
		t.Fatalf("the halt carries %d diagnostic(s) for 2 offending nodes: %v", len(leaves), leaves)
	}
	for _, want := range []string{"first-recipe at .spec.source", "second-recipe at .spec.source"} {
		held := false
		for _, leaf := range leaves {
			if strings.Contains(leaf, want) {
				held = true
				break
			}
		}
		if !held {
			t.Errorf("no single diagnostic is positioned at %q, so the halt reports fewer than every "+
				"offending node, or reports them somewhere an author cannot navigate to: %v", want, leaves)
		}
	}
	if text := err.Error(); !strings.Contains(text, "Recipe source entries that name neither authoring mode") {
		t.Errorf("the findings are not grouped under the rule's own lead: %s", text)
	}
}

// TestRecipeSourceRule_ReadsBothFactsRatherThanOne pins the split the declaration
// rests on, at the view rather than through the rule.
//
// One fact answers whether the entry was WRITTEN and the other whether it NAMES
// anything, and the rule is the pair. A single fact answering both would report two
// authoring situations as one, and the row that separates them is the empty entry:
// it is the only input for which the two facts disagree.
func TestRecipeSourceRule_ReadsBothFactsRatherThanOne(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		source      any
		wantWritten bool
		wantNames   bool
	}{
		{name: "no entry at all", source: nil, wantWritten: false, wantNames: false},
		{name: "an entry written and left empty", source: "", wantWritten: true, wantNames: false},
		{name: "an entry naming a path", source: "./chart", wantWritten: true, wantNames: true},
		{name: "a structured entry", source: map[string]any{"url": "oci://example.test/c"}, wantWritten: true, wantNames: true},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			const node = "api"
			recipes := map[string]*types.Recipe{node: recipeSourceProbe(tc.source)}
			nodes := types.OrderedMap[types.IRNode]{}
			nodes.Set(node, types.IRNode{})
			graph := &types.IRGraph{Spec: types.IRSpec{Nodes: nodes}}

			view := newDescribedView(
				context.Background(), graph, stubGraphFacts{},
				recipes, RecipeLookup{}, nil, newStepDescriptorMemo(), nil, nil,
				nil, nil, nil, nil,
			)
			if view == nil {
				t.Fatal("the view was not built")
			}
			if got := view.RecipeSourceEntryWritten(node, "source"); got != tc.wantWritten {
				t.Errorf("the entry-written fact answered %v; want %v", got, tc.wantWritten)
			}
			if got := view.NodeDeclaresAuthoredSource(node); got != tc.wantNames {
				t.Errorf("the entry-names-something fact answered %v; want %v", got, tc.wantNames)
			}
			// A coordinate naming any other position answers false, which is what
			// leaves a rule over this axis declining what it does not govern.
			if view.RecipeSourceEntryWritten(node, "valuesMapping") {
				t.Error("the entry-written fact answered for a position other than the source entry")
			}
		})
	}
}

// TestRecipeSourceKindIsDerivedWithItsCoordinates holds the kind the rule is
// declared over against the derived set, so a fact declaration moved or removed
// stops this file compiling a claim about a kind that no longer exists.
func TestRecipeSourceKindIsDerivedWithItsCoordinates(t *testing.T) {
	t.Parallel()

	var info constraint.SubjectKindInfo
	var held bool
	for _, candidate := range constraint.SubjectKinds {
		if candidate.Kind == constraint.SubjectNodeRecipeSourceField {
			info, held = candidate, true
			break
		}
	}
	if !held {
		t.Fatal("the kind the recipe-source rule is declared over is not in the derived set")
	}
	want := []string{"node", "recipe-source-field"}
	if len(info.Coordinates) != len(want) {
		t.Fatalf("the kind declares %v; want %v", info.Coordinates, want)
	}
	for i := range want {
		if info.Coordinates[i] != want[i] {
			t.Errorf("coordinate %d is %q; want %q", i, info.Coordinates[i], want[i])
		}
	}
	if info.Traversal != constraint.TraversalPull {
		t.Errorf("the kind is reached by the %q model; want %q — the framework enumerates the position "+
			"rather than being handed one", info.Traversal, constraint.TraversalPull)
	}
	// No comparison against the import kind's constant is made here: two distinct
	// constants cannot be equal, so such an assertion could not fail and would be
	// decoration. What separates the two axes is which coordinates each ENUMERATES,
	// and that is measured over the real enumeration in the proof above.
}
