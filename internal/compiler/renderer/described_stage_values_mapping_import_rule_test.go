// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The proofs in this file cover the two rules declared over the values-mapping
// library import a node's own Recipe writes: that the production site reaches
// them, how they compare with the resolution they stand ahead of, that the
// traversal enumerates a non-zero subject set on a legal input, that the entry
// rule declines a field whose library was not found, that neither is invoked over
// a field carrying no library reference, and that a finding carries the authored
// position the declaration exists to add.
//
// Two of them are load-bearing and neither is a formality.
//
// The COMPARISON proof, because the rules decide from declarations alone what the
// resolution decides while resolving. It is stated as a per-class expectation for
// each side rather than as an agreement, because the two deliberately differ on
// two classes and an agreement assertion would have to be weakened to admit them —
// which would stop it saying anything about the classes where they must agree.
//
// The ENUMERATION proof, because this kind exists precisely to be reachable for a
// field whose import fails. A rule invoked zero times and a rule that found nothing
// are indistinguishable from the outcome, so a verdict taken over a walk that
// reached no subject would certify nothing at all.

// valuesMappingImportLibraries is the resolved MappingLibrary set every row below
// is driven against: one library declaring one entry.
func valuesMappingImportLibraries() map[string]types.MappingLibrary {
	return map[string]types.MappingLibrary{
		"Shared": mappingLibraryFixture("Shared", map[string]string{
			"Core": `image: "{{ .context.instance_id }}"`,
		}),
	}
}

// valuesMappingImportRecipe is the authored document both paths are driven with,
// so the comparison is over one Recipe rather than over two fixtures that agree
// today.
//
// It declares a source because the authoring surface requires one wherever a
// values mapping is declared at all, so a Recipe without one is a shape no build
// reaches this question over.
func valuesMappingImportRecipe(ref string) *types.Recipe {
	r := minimalRecipe("values-mapping-recipe")
	r.Spec.Source = "./chart"
	r.Spec.ValuesMapping = types.ValuesMapping{Import: ref}
	return r
}

// declaredValuesMappingHalt drives the two declared rules through the PRODUCTION
// invocation site over one authored Recipe, with the real presentation and the
// real enumeration, and returns the halt.
//
// Both sides are production code: the presentation is the one the render builds
// and the axis is the one the render derives, so the verdict is the declared
// route's rather than a second reading of the Recipe assembled here.
//
// The subject count is measured before the verdict is taken, because a verdict
// from a walk that reached no subject reports exactly what an admitted field
// reports — and this kind exists to be reachable for the fields whose resolution
// fails, which is the claim that would otherwise go unproved.
func declaredValuesMappingHalt(
	t *testing.T, recipe *types.Recipe, libs map[string]types.MappingLibrary,
) error {
	t.Helper()
	const node = "api"
	recipes := map[string]*types.Recipe{node: recipe}

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(node, types.IRNode{})
	graph := &types.IRGraph{Spec: types.IRSpec{Nodes: nodes}}

	view := newDescribedView(
		context.Background(), graph, stubGraphFacts{},
		recipes, RecipeLookup{}, nil, newStepDescriptorMemo(), nil, nil,
		nil, nil, libs, nil,
	)
	if view == nil {
		t.Fatal("the view was not built; there is nothing for the rules to read")
	}
	axes := newDescribedStageCoordinates(graph, localsAxis{nodes: []string{node}}, recipes, RecipeLookup{}, nil)
	if axes == nil {
		t.Fatal("the enumeration refused to build over a graph and a bound Recipe")
	}
	if got, want := len(axes.RecipeImportFields(node)), len(RecipeImportFields(recipe)); got != want {
		t.Fatalf("the axis presents %d import field(s) for a Recipe supplying %d by import; "+
			"the verdict below would certify nothing", got, want)
	}
	return runValuesMappingImportRule(context.Background(), view, axes)
}

// resolvedValuesMappingHalt drives the SAME authored Recipe through the render
// sub-pass the declared rules stand ahead of, and returns its halt.
//
// It is the other side of the comparison, and it is the production sub-pass rather
// than a direct call on the resolver: what the rules have to be compared with is
// what a build does, and the sub-pass is where the field's authoring mode is
// dispatched on before the resolver is reached at all.
func resolvedValuesMappingHalt(
	t *testing.T, recipe *types.Recipe, libs map[string]types.MappingLibrary,
) error {
	t.Helper()
	state := NewPhaseBState(recipe, "api", "default", "recipe.yaml")
	state.MappingLibraries = libs
	return phaseB1(minimalCtx(), state)
}

// valuesMappingImportCase is one authored reference plus the condition each side
// must reach over it. A nil expectation is silence from that side.
//
// The two expectations are separate fields rather than one shared value, because
// the declaration deliberately covers less than the resolution: two of the classes
// below are refused before any render begins, so a rule over them at this stage
// could not be invoked from a build at all and is not declared. Folding the two
// columns into one would force those rows to be dropped or the assertion weakened,
// and either would stop this table saying anything about the rows where the two
// must match.
type valuesMappingImportCase struct {
	name string
	ref  string

	// wantDeclared is the condition the declared rules reach, nil where they are
	// silent by design.
	wantDeclared error

	// wantResolved is the condition the render's own resolution reaches.
	wantResolved error

	// why records, for a row where the two differ, what makes the difference
	// correct. A row with no reason is a row where they must match.
	why string
}

func valuesMappingImportCases() []valuesMappingImportCase {
	return []valuesMappingImportCase{
		{
			name: "an entry that resolves is admitted by both",
			ref:  "Shared#/mappings/Core",
		},
		{
			name: "a version floor written with a colon resolves",
			ref:  "Shared:1.0.0#/mappings/Core",
		},
		{
			name:         "an entry the library does not declare",
			ref:          "Shared#/mappings/Missing",
			wantDeclared: ErrMappingEntryNotFound(),
			wantResolved: ErrMappingEntryNotFound(),
		},
		{
			name:         "a reference naming no entry after the fragment",
			ref:          "Shared#/mappings/",
			wantDeclared: ErrMappingImportShape(),
			wantResolved: ErrMappingImportShape(),
		},
		{
			name:         "a library nothing declares",
			ref:          "Absent#/mappings/Core",
			wantResolved: ErrMappingLibraryNotFound(),
			why: "a reference naming a library nothing publishes is refused as the Release's " +
				"dependencies are resolved, which precedes the render on every surface that " +
				"renders — so no rule over it is declared at this stage, and the entry rule " +
				"declines rather than reporting an entry inside a library that is not there",
		},
		{
			name:         "the retired version separator",
			ref:          "Shared@1.0.0#/mappings/Core",
			wantResolved: ErrMappingImportShape(),
			why: "the separator makes the reference unusable as a dependency, so it too is " +
				"refused before any render begins; the reference splits cleanly here, which is " +
				"why the shape rule is silent rather than special-casing it",
		},
		{
			name:         "a reference carrying a registry host",
			ref:          "oci://registry.example/Shared#/mappings/Core",
			wantResolved: ErrHostInLibraryImport(),
			why: "a host-bearing reference is its own condition, raised where the field is " +
				"consumed; the fact answers empty for it so neither rule below judges it",
		},
	}
}

// TestValuesMappingImportRules_CompareWithTheResolutionTheyStandAheadOf drives one
// authored Recipe through both routes per class and holds each side to its own
// expectation.
//
// Where the two must match, a difference in either direction is a defect the
// declaration introduced: the rules halt a build before the sub-pass runs, so a
// rule refusing what the resolution admits stops a build that renders today, and a
// rule admitting what it refuses leaves the resolution's own arm doing the work the
// declaration claims to do.
func TestValuesMappingImportRules_CompareWithTheResolutionTheyStandAheadOf(t *testing.T) {
	t.Parallel()

	for _, tc := range valuesMappingImportCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			recipe := valuesMappingImportRecipe(tc.ref)
			libs := valuesMappingImportLibraries()

			declared := declaredValuesMappingHalt(t, recipe, libs)
			resolved := resolvedValuesMappingHalt(t, recipe, libs)

			switch {
			case tc.wantDeclared == nil && declared != nil:
				t.Errorf("the declared rules refused %q: %v", tc.ref, declared)
			case tc.wantDeclared != nil && declared == nil:
				t.Errorf("the declared rules admitted %q; want %v", tc.ref, tc.wantDeclared)
			case tc.wantDeclared != nil && !errors.Is(declared, tc.wantDeclared):
				t.Errorf("the declared rules reported %v; want %v", declared, tc.wantDeclared)
			}

			switch {
			case tc.wantResolved == nil && resolved != nil:
				t.Errorf("the resolution refused %q: %v", tc.ref, resolved)
			case tc.wantResolved != nil && resolved == nil:
				t.Errorf("the resolution admitted %q; want %v", tc.ref, tc.wantResolved)
			case tc.wantResolved != nil && !errors.Is(resolved, tc.wantResolved):
				t.Errorf("the resolution reported %v; want %v", resolved, tc.wantResolved)
			}

			// A row whose two sides differ carries the reason the difference is
			// correct, and a row whose sides match carries none. Asserting the pairing
			// is what stops a future divergence being absorbed by adding a reason
			// string, and stops a stale reason outliving the difference it explained.
			differ := (tc.wantDeclared == nil) != (tc.wantResolved == nil)
			if differ && tc.why == "" {
				t.Errorf("the two routes differ over %q and the row records no reason", tc.ref)
			}
			if !differ && tc.why != "" {
				t.Errorf("the two routes agree over %q and the row still carries a reason: %s", tc.ref, tc.why)
			}
		})
	}
}

// TestValuesMappingImportRules_CarryTheAuthoredPosition is clause-for-clause the
// difference the declaration makes, measured rather than asserted: the render's own
// refusal names the Recipe and stops, while the declared finding names the field
// inside it.
//
// The locator names the field in full rather than stopping at its container,
// because a Recipe's spec-root keys are fixed by the authoring surface and carry no
// locator separator. So a rule over this kind owes no captioned stand-in for a
// position it could not reach, which is where this axis differs from the artifacts
// one beside it.
func TestValuesMappingImportRules_CarryTheAuthoredPosition(t *testing.T) {
	t.Parallel()

	recipe := valuesMappingImportRecipe("Shared#/mappings/Missing")
	libs := valuesMappingImportLibraries()

	declared := declaredValuesMappingHalt(t, recipe, libs)
	if declared == nil {
		t.Fatal("the declared rules admitted an entry the library does not declare")
	}
	resolved := resolvedValuesMappingHalt(t, recipe, libs)
	if resolved == nil {
		t.Fatal("the resolution admitted an entry the library does not declare")
	}

	before, after := resolved.Error(), declared.Error()
	t.Logf("before (the render's own refusal): %s", before)
	t.Logf("after  (the declared rule):        %s", after)

	// The position. The render's refusal names a document and no key inside it;
	// the declaration names the field.
	if strings.Contains(before, " at .") {
		t.Errorf("the render's refusal already carries an authored locator, so the declaration adds none: %s", before)
	}
	if !strings.Contains(after, "values-mapping-recipe at .spec.valuesMapping") {
		t.Errorf("the declared finding does not carry the framework-supplied position: %s", after)
	}

	// The detail. The entry the sentence is about stands bare; the library it was
	// looked for in is captioned, because an entry name means nothing without it.
	if !strings.Contains(after, `"Missing"`) {
		t.Errorf("the finding does not name the entry its sentence is about: %s", after)
	}
	if !strings.Contains(after, `MappingLibrary "Shared"`) {
		t.Errorf("the finding does not caption the library the entry was looked for in: %s", after)
	}

	// And the identity, which is read off the chain rather than off the rendering.
	// A single-line rendering is the category, the position and the sentence; no
	// condition's short code appears in it, before or after, because the code is
	// carried by the registered condition the chain names and is composed into the
	// head of a diagnostic by the surface that formats one for a terminal. A
	// substring assertion over this string would therefore pin a property the
	// rendering has for no condition, and pass only where the code happened to occur
	// inside the sentence.
	if code, ok := cerrors.ShortCodeOf(declared); !ok || code != "E0044" {
		t.Errorf("the finding carries short code %q (present=%v); want E0044 — the condition the "+
			"declaration registers is what a formatter names the diagnostic by", code, ok)
	}
}

// TestValuesMappingImportRules_EnumerateEveryImportedField proves the rule is invoked,
// driven rather than argued: the traversal reaches a non-zero subject set on a
// LEGAL input, so a rule that found nothing is distinguishable from a rule nothing
// invoked.
//
// It also measures the point of the kind. The template-field axis is built from the
// text a Recipe writes in this document, so it presents nothing at all for a field
// supplied by an import; this axis presents exactly those fields, which is why the
// two sets below are disjoint in the stated direction.
func TestValuesMappingImportRules_EnumerateEveryImportedField(t *testing.T) {
	t.Parallel()

	const node = "api"
	recipe := valuesMappingImportRecipe("Shared#/mappings/Core")
	// The workload is supplied by an import too, so the axis presents a field no
	// rule below will judge: an axis narrowed to the values mapping would make every
	// "no finding" result unreadable, and the rules' own decline would go unproved.
	recipe.Spec.Workload = types.WorkloadTemplate{Import: "Base#/workloads/Stateless"}

	recipes := map[string]*types.Recipe{node: recipe}
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(node, types.IRNode{})
	graph := &types.IRGraph{Spec: types.IRSpec{Nodes: nodes}}

	axes := newDescribedStageCoordinates(graph, localsAxis{nodes: []string{node}}, recipes, RecipeLookup{}, nil)
	if axes == nil {
		t.Fatal("the enumeration refused to build over a graph and a bound Recipe")
	}
	fields := axes.RecipeImportFields(node)
	if len(fields) != 2 {
		t.Fatalf("the axis presented %d field(s); the Recipe supplies 2 by import", len(fields))
	}
	// The enumeration's order is fixed rather than the document's, so the sequence
	// is asserted rather than the set.
	if fields[0].Value != "valuesMapping" || fields[1].Value != "workload" {
		t.Errorf("the axis presented %q then %q; the enumeration declares valuesMapping then workload",
			fields[0].Value, fields[1].Value)
	}
	// Each coordinate is positioned at the Recipe, which is the document an author
	// edits to change the field.
	for _, field := range fields {
		if field.SourceFile != recipe.Metadata.Name {
			t.Errorf("field %q is positioned at %q; want the Recipe %q",
				field.Value, field.SourceFile, recipe.Metadata.Name)
		}
	}

	// The inline side of the same block presents nothing here, which is what makes
	// the two axes disjoint rather than two readings of one set.
	inline := axes.NodeTemplateFields(node)
	for _, field := range inline {
		if field.Value == "valuesMapping" || field.Value == "workload" {
			t.Errorf("the inline template-field axis presented %q, which this Recipe supplies by "+
				"import; the two axes would then answer for one field twice", field.Value)
		}
	}

	// And the legal input is admitted, so the non-zero count above is a statement
	// about a walk that ran rather than about one that raised.
	if err := declaredValuesMappingHalt(t, recipe, valuesMappingImportLibraries()); err != nil {
		t.Errorf("a legal Recipe was refused: %v", err)
	}
}

// TestValuesMappingImportRules_DeclineAFieldWhoseLibraryIsAbsent is the cascade
// assertion: one authoring mistake produces at most one finding.
//
// The library is the outer fault and no rule at this stage reports it, so the entry
// rule has to stay silent rather than reporting an entry missing from a library
// that is not there — which would name the inner fault for a reader whose edit is
// the outer one.
func TestValuesMappingImportRules_DeclineAFieldWhoseLibraryIsAbsent(t *testing.T) {
	t.Parallel()

	recipe := valuesMappingImportRecipe("Absent#/mappings/Core")
	if err := declaredValuesMappingHalt(t, recipe, valuesMappingImportLibraries()); err != nil {
		t.Errorf("a field whose library was not found drew a finding from a rule that should decline: %v", err)
	}

	// The calibration: the same library, present, with the entry still absent draws
	// the entry finding. Without it a rule that declined everything would pass the
	// assertion above and say nothing.
	present := valuesMappingImportRecipe("Shared#/mappings/Core")
	present.Spec.ValuesMapping = types.ValuesMapping{Import: "Shared#/mappings/Absent"}
	err := declaredValuesMappingHalt(t, present, valuesMappingImportLibraries())
	if !errors.Is(err, ErrMappingEntryNotFound()) {
		t.Errorf("the entry rule reported %v over a declared library missing the entry; want %v",
			err, ErrMappingEntryNotFound())
	}
}

// TestValuesMappingImportRules_AreNotInvokedOverAFieldCarryingNoLibraryReference
// pins the fact's own narrowing: the axis presents what the document writes, and
// the reference fact answers empty for every authoring form the rules do not
// govern, so the rules decline rather than judging them.
func TestValuesMappingImportRules_AreNotInvokedOverAFieldCarryingNoLibraryReference(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		build func() *types.Recipe
	}{
		{
			name: "a values mapping supplied by a file of the workspace",
			build: func() *types.Recipe {
				return valuesMappingImportRecipe("./mappings/values.tmpl")
			},
		},
		{
			name: "a values mapping written inline",
			build: func() *types.Recipe {
				r := minimalRecipe("values-mapping-recipe")
				r.Spec.Source = "./chart"
				r.Spec.ValuesMapping = types.ValuesMapping{Inline: `image: "x"`}
				return r
			},
		},
		{
			name: "a workload supplied by a library import, which this axis presents and no rule here judges",
			build: func() *types.Recipe {
				r := minimalRecipe("values-mapping-recipe")
				r.Spec.Workload = types.WorkloadTemplate{Import: "Absent#/workloads/Missing"}
				return r
			},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := declaredValuesMappingHalt(t, tc.build(), valuesMappingImportLibraries()); err != nil {
				t.Errorf("a rule judged a field it does not govern: %v", err)
			}
		})
	}
}

// TestValuesMappingImportRules_ReportEveryOffendingNode measures the breadth the
// declaration adds: the render resolves one node at a time and stops at the first
// that fails, while the rules are invoked once per node and report all of them,
// each at its own position.
//
// # Why the breadth is read from the diagnostics beneath the halt
//
// Several findings come back as an envelope over one diagnostic per finding, and
// the envelope's own single-line rendering is its category and the wording the rule
// supplied — never the findings underneath. That is deliberate: a rendering is
// re-wrapped on its way up, so a multi-line one would carry its layout into every
// frame that wraps it, and the multi-line shape belongs to the surface writing to a
// terminal. An assertion over the envelope's line can therefore only ever see the
// wording this file's own caller passed in, whatever the rules found — it would
// report a rule that reported nothing exactly as it reports a rule that reported
// everything.
//
// The single-finding case hides this, because one objection is returned as itself
// rather than as a set of one: the proof above this one reads a position straight
// off a one-finding halt for that reason, and the two are not the same read.
//
// The per-finding pairing below is the point of the walk rather than a stricter
// spelling of it. One diagnostic listing every offender would satisfy a search for
// the same names across the whole text while carrying a position an author can
// navigate to for none of them, which is the thing the declaration exists to add.
func TestValuesMappingImportRules_ReportEveryOffendingNode(t *testing.T) {
	t.Parallel()

	first := valuesMappingImportRecipe("Shared#/mappings/MissingOne")
	first.Metadata.Name = "first-recipe"
	second := valuesMappingImportRecipe("Shared#/mappings/MissingTwo")
	second.Metadata.Name = "second-recipe"

	recipes := map[string]*types.Recipe{"api": first, "web": second}
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{})
	nodes.Set("web", types.IRNode{})
	graph := &types.IRGraph{Spec: types.IRSpec{Nodes: nodes}}

	view := newDescribedView(
		context.Background(), graph, stubGraphFacts{},
		recipes, RecipeLookup{}, nil, newStepDescriptorMemo(), nil, nil,
		nil, nil, valuesMappingImportLibraries(), nil,
	)
	if view == nil {
		t.Fatal("the view was not built")
	}
	axes := newDescribedStageCoordinates(
		graph, localsAxis{nodes: []string{"api", "web"}}, recipes, RecipeLookup{}, nil)
	if axes == nil {
		t.Fatal("the enumeration refused to build")
	}

	err := runValuesMappingImportRule(context.Background(), view, axes)
	if err == nil {
		t.Fatal("two Recipes naming entries no library declares were both admitted")
	}
	t.Logf("the envelope: %s", err.Error())
	leaves := haltLeafMessages(err)
	for i, leaf := range leaves {
		t.Logf("finding %d: %s", i+1, leaf)
	}
	if len(leaves) != 2 {
		t.Fatalf("the halt carries %d diagnostic(s) for 2 offending nodes: %v", len(leaves), leaves)
	}
	for _, want := range []struct{ recipe, entry string }{
		{recipe: "first-recipe", entry: "MissingOne"},
		{recipe: "second-recipe", entry: "MissingTwo"},
	} {
		held := false
		for _, leaf := range leaves {
			if strings.Contains(leaf, want.recipe+" at .spec.valuesMapping") &&
				strings.Contains(leaf, `"`+want.entry+`"`) {
				held = true
				break
			}
		}
		if !held {
			t.Errorf("no single diagnostic names entry %q at the position of %q, so the halt reports "+
				"fewer than every offending node, or reports them somewhere an author cannot navigate "+
				"to: %v", want.entry, want.recipe, leaves)
		}
	}
	// The envelope groups them under this rule's own wording, which is what a reader
	// meets above the findings.
	if text := err.Error(); !strings.Contains(text, "values-mapping imports the compiler cannot resolve") {
		t.Errorf("the findings are not grouped under the rule's own lead: %s", text)
	}
}

// TestValuesMappingImportKindIsDerivedWithItsCoordinates holds the kind the two
// rules are declared over against the derived set, so a fact declaration moved or
// removed stops this file compiling a claim about a kind that no longer exists.
func TestValuesMappingImportKindIsDerivedWithItsCoordinates(t *testing.T) {
	t.Parallel()

	var info constraint.SubjectKindInfo
	var held bool
	for _, candidate := range constraint.SubjectKinds {
		if candidate.Kind == constraint.SubjectNodeRecipeImportField {
			info, held = candidate, true
			break
		}
	}
	if !held {
		t.Fatal("the kind the values-mapping import rules are declared over is not in the derived set")
	}
	want := []string{"node", "recipe-import-field"}
	if len(info.Coordinates) != len(want) {
		t.Fatalf("the kind declares %v; want %v", info.Coordinates, want)
	}
	for i := range want {
		if info.Coordinates[i] != want[i] {
			t.Errorf("coordinate %d is %q; want %q", i, info.Coordinates[i], want[i])
		}
	}
	if info.Traversal != constraint.TraversalPull {
		t.Errorf("the kind is reached by the %q model; want %q — the framework enumerates the fields "+
			"rather than being handed one", info.Traversal, constraint.TraversalPull)
	}
}
