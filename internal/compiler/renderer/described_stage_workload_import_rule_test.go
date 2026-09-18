// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"errors"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The proofs in this file cover the two rules declared over the workload library
// import a node's own Recipe writes: that the production site reaches them, how they
// compare with the resolution they stand ahead of, that the traversal enumerates a
// non-zero subject set on a legal input, that the entry rule declines a field whose
// library was not found, that neither is invoked over a field carrying no library
// reference, and that a finding carries the authored position the declaration exists
// to add.
//
// Two of them are load-bearing and neither is a formality.
//
// The COMPARISON proof, because the rules decide from declarations alone what the
// resolution decides while resolving. It is stated as a per-class expectation for
// each side rather than as an agreement, because the two deliberately differ on two
// classes and an agreement assertion would have to be weakened to admit them.
//
// The ENUMERATION proof, because the kind exists precisely to be reachable for a
// field whose import fails. A rule invoked zero times and a rule that found nothing
// are indistinguishable from the outcome.
//
// # What is deliberately NOT asserted here
//
// That the kind is derived with its two coordinates. It is one derived value and the
// values-mapping proofs beside these already hold it against the derived set; a
// second assertion over the same value would be a copy agreeing on the day it was
// written, and the two rules here are declared over that same kind rather than over
// one of their own.

// workloadImportBody is the Workload document the resolvable entry below carries. It
// is a complete document rather than a fragment because the render's own sub-pass
// evaluates, parses and validates what the import resolves to, so a fragment would
// make the comparison's admitted rows fail on a fault that is not the import's.
const workloadImportBody = `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: app:1.0
`

// workloadImportLibraries is the resolved WorkloadLibrary set every row below is
// driven against: one library declaring one entry.
func workloadImportLibraries() map[string]types.WorkloadLibrary {
	return map[string]types.WorkloadLibrary{
		"Base": workloadLibraryFixture("Base", map[string]string{
			"Stateless": workloadImportBody,
		}),
	}
}

// workloadImportRecipe is the authored document both paths are driven with, so the
// comparison is over one Recipe rather than over two fixtures that agree today.
func workloadImportRecipe(ref string) *types.Recipe {
	r := minimalRecipe("workload-recipe")
	r.Spec.Workload = types.WorkloadTemplate{Import: ref}
	return r
}

// declaredWorkloadHalt drives the two declared rules through the PRODUCTION
// invocation site over one authored Recipe, with the real presentation and the real
// enumeration, and returns the halt.
//
// Both sides are production code: the presentation is the one the render builds and
// the axis is the one the render derives, so the verdict is the declared route's
// rather than a second reading of the Recipe assembled here.
//
// The subject count is measured before the verdict is taken, because a verdict from a
// walk that reached no subject reports exactly what an admitted field reports — and
// this kind exists to be reachable for the fields whose resolution fails, which is the
// claim that would otherwise go unproved.
func declaredWorkloadHalt(
	t *testing.T, recipe *types.Recipe, libs map[string]types.WorkloadLibrary,
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
		nil, nil, nil, libs,
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
	return runWorkloadImportRule(context.Background(), view, axes)
}

// resolvedWorkloadHalt drives the SAME authored Recipe through the render sub-pass
// the declared rules stand ahead of, and returns its halt.
//
// It is the other side of the comparison, and it is the production sub-pass rather
// than a direct call on the resolver: what the rules have to be compared with is what
// a build does, and the sub-pass is where the field's authoring mode is dispatched on
// before the resolver is reached at all.
func resolvedWorkloadHalt(
	t *testing.T, recipe *types.Recipe, libs map[string]types.WorkloadLibrary,
) error {
	t.Helper()
	state := NewPhaseBState(recipe, "api", "default", "recipe.yaml")
	state.WorkloadLibraries = libs
	return phaseB2(minimalCtx(), state, types.OrderedMap[types.AttachmentSlot]{})
}

// workloadImportCase is one authored reference plus the condition each side must
// reach over it. A nil expectation is silence from that side.
//
// The two expectations are separate fields rather than one shared value, because the
// declaration deliberately covers less than the resolution: two of the classes below
// are refused before any render begins, so a rule over them at this stage could not be
// invoked from a build at all and is not declared.
type workloadImportCase struct {
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

func workloadImportCases() []workloadImportCase {
	return []workloadImportCase{
		{
			name: "an entry that resolves is admitted by both",
			ref:  "Base#/workloads/Stateless",
		},
		{
			name: "a version floor written with a colon resolves",
			ref:  "Base:1.0.0#/workloads/Stateless",
		},
		{
			name:         "an entry the library does not declare",
			ref:          "Base#/workloads/Missing",
			wantDeclared: ErrWorkloadEntryNotFound(),
			wantResolved: ErrWorkloadEntryNotFound(),
		},
		{
			name:         "a reference naming no entry after the fragment",
			ref:          "Base#/workloads/",
			wantDeclared: ErrWorkloadImportShape(),
			wantResolved: ErrWorkloadImportShape(),
		},
		{
			name:         "a reference naming no library before the fragment",
			ref:          ":1.0.0#/workloads/Stateless",
			wantDeclared: ErrWorkloadImportShape(),
			wantResolved: ErrWorkloadImportShape(),
		},
		{
			name:         "a library nothing declares",
			ref:          "Absent#/workloads/Stateless",
			wantResolved: ErrWorkloadLibraryNotFound(),
			why: "a reference naming a library nothing publishes is refused as the Release's " +
				"dependencies are resolved, which precedes the render on every surface that " +
				"renders — so no rule over it is declared at this stage, and the entry rule " +
				"declines rather than reporting an entry inside a library that is not there",
		},
		{
			name:         "the retired version separator",
			ref:          "Base@1.0.0#/workloads/Stateless",
			wantResolved: ErrWorkloadImportShape(),
			why: "the separator makes the reference unusable as a dependency, so it too is " +
				"refused before any render begins; the reference splits cleanly here, which is " +
				"why the shape rule is silent rather than special-casing it",
		},
		{
			name:         "a reference carrying a registry host",
			ref:          "oci://registry.example/Base#/workloads/Stateless",
			wantResolved: ErrHostInLibraryImport(),
			why: "a host-bearing reference is its own condition, raised where the field is " +
				"consumed; the fact answers empty for it so neither rule below judges it",
		},
	}
}

// TestWorkloadImportRules_CompareWithTheResolutionTheyStandAheadOf drives one authored
// Recipe through both routes per class and holds each side to its own expectation.
//
// Where the two must match, a difference in either direction is a defect the
// declaration introduced: the rules halt a build before the sub-pass runs, so a rule
// refusing what the resolution admits stops a build that renders today, and a rule
// admitting what it refuses leaves the resolution's own arm doing the work the
// declaration claims to do.
func TestWorkloadImportRules_CompareWithTheResolutionTheyStandAheadOf(t *testing.T) {
	t.Parallel()

	for _, tc := range workloadImportCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			recipe := workloadImportRecipe(tc.ref)
			libs := workloadImportLibraries()

			declared := declaredWorkloadHalt(t, recipe, libs)
			resolved := resolvedWorkloadHalt(t, recipe, libs)

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

// TestWorkloadImportRules_CarryTheAuthoredPosition is clause-for-clause the difference
// the declaration makes, measured rather than asserted: the render's own refusal names
// the Recipe and stops, while the declared finding names the field inside it.
//
// The locator names the field in full rather than stopping at its container, because a
// Recipe's spec-root keys are fixed by the authoring surface and carry no locator
// separator. So a rule over this kind owes no captioned stand-in for a position it
// could not reach.
//
// The halt read here carries ONE finding, so the rendering is the finding's own rather
// than an envelope over several. The multi-finding read is the proof further down, and
// the two are not the same read.
func TestWorkloadImportRules_CarryTheAuthoredPosition(t *testing.T) {
	t.Parallel()

	recipe := workloadImportRecipe("Base#/workloads/Missing")
	libs := workloadImportLibraries()

	declared := declaredWorkloadHalt(t, recipe, libs)
	if declared == nil {
		t.Fatal("the declared rules admitted an entry the library does not declare")
	}
	resolved := resolvedWorkloadHalt(t, recipe, libs)
	if resolved == nil {
		t.Fatal("the resolution admitted an entry the library does not declare")
	}

	before, after := resolved.Error(), declared.Error()
	t.Logf("before (the render's own refusal): %s", before)
	t.Logf("after  (the declared rule):        %s", after)

	// The position. The render's refusal names a document and no key inside it; the
	// declaration names the field.
	if strings.Contains(before, " at .") {
		t.Errorf("the render's refusal already carries an authored locator, so the declaration adds none: %s", before)
	}
	if !strings.Contains(after, "workload-recipe at .spec.workload") {
		t.Errorf("the declared finding does not carry the framework-supplied position: %s", after)
	}

	// The detail. The entry the sentence is about stands bare; the library it was
	// looked for in is captioned, because an entry name means nothing without it.
	if !strings.Contains(after, `"Missing"`) {
		t.Errorf("the finding does not name the entry its sentence is about: %s", after)
	}
	if !strings.Contains(after, `WorkloadLibrary "Base"`) {
		t.Errorf("the finding does not caption the library the entry was looked for in: %s", after)
	}

	// And the identity, which is read off the chain rather than off the rendering. A
	// single-line rendering is the category, the position and the sentence; no
	// condition's short code appears in it, before or after, because the code is carried
	// by the registered condition the chain names and is composed into the head of a
	// diagnostic by the surface that formats one for a terminal. A substring assertion
	// over this string would therefore pin a property the rendering has for no
	// condition, and pass only where the code happened to occur inside the sentence.
	if code, ok := cerrors.ShortCodeOf(declared); !ok || code != "E0063" {
		t.Errorf("the finding carries short code %q (present=%v); want E0063 — the condition the "+
			"declaration registers is what a formatter names the diagnostic by", code, ok)
	}
}

// TestWorkloadImportRules_EnumerateEveryImportedField is the refutation driven rather
// than argued: the traversal reaches a non-zero subject set on a LEGAL input, so a rule
// that found nothing is distinguishable from a rule nothing invoked.
//
// It also measures the point of the kind. The template-field axis is built from the
// text a Recipe writes in this document, so it presents nothing at all for a field
// supplied by an import; this axis presents exactly those fields, which is why the two
// sets below are disjoint in the stated direction.
func TestWorkloadImportRules_EnumerateEveryImportedField(t *testing.T) {
	t.Parallel()

	const node = "api"
	recipe := workloadImportRecipe("Base#/workloads/Stateless")
	// The values mapping is supplied by an import too, so the axis presents a field no
	// rule below will judge: an axis narrowed to the workload would make every "no
	// finding" result unreadable, and the rules' own decline would go unproved.
	recipe.Spec.Source = "./chart"
	recipe.Spec.ValuesMapping = types.ValuesMapping{Import: "Shared#/mappings/Core"}

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
	// The enumeration's order is fixed rather than the document's, so the sequence is
	// asserted rather than the set.
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

	// The inline side of the same block presents nothing here, which is what makes the
	// two axes disjoint rather than two readings of one set.
	inline := axes.NodeTemplateFields(node)
	for _, field := range inline {
		if field.Value == "valuesMapping" || field.Value == "workload" {
			t.Errorf("the inline template-field axis presented %q, which this Recipe supplies by "+
				"import; the two axes would then answer for one field twice", field.Value)
		}
	}

	// And the legal input is admitted, so the non-zero count above is a statement about
	// a walk that ran rather than about one that raised. The values mapping names a
	// library this set does not hold, which the workload rules must not judge.
	if err := declaredWorkloadHalt(t, recipe, workloadImportLibraries()); err != nil {
		t.Errorf("a Recipe whose workload import resolves was refused: %v", err)
	}
}

// TestWorkloadImportRules_DeclineAFieldWhoseLibraryIsAbsent is the cascade assertion:
// one authoring mistake produces at most one finding.
//
// The library is the outer fault and no rule at this stage reports it, so the entry
// rule has to stay silent rather than reporting an entry missing from a library that is
// not there — which would name the inner fault for a reader whose edit is the outer one.
func TestWorkloadImportRules_DeclineAFieldWhoseLibraryIsAbsent(t *testing.T) {
	t.Parallel()

	recipe := workloadImportRecipe("Absent#/workloads/Stateless")
	if err := declaredWorkloadHalt(t, recipe, workloadImportLibraries()); err != nil {
		t.Errorf("a field whose library was not found drew a finding from a rule that should decline: %v", err)
	}

	// The calibration: the same library, present, with the entry still absent draws the
	// entry finding. Without it a rule that declined everything would pass the assertion
	// above and say nothing.
	present := workloadImportRecipe("Base#/workloads/Absent")
	err := declaredWorkloadHalt(t, present, workloadImportLibraries())
	if !errors.Is(err, ErrWorkloadEntryNotFound()) {
		t.Errorf("the entry rule reported %v over a declared library missing the entry; want %v",
			err, ErrWorkloadEntryNotFound())
	}
}

// TestWorkloadImportRules_AreNotInvokedOverAFieldCarryingNoLibraryReference pins the
// fact's own narrowing: the axis presents what the document writes, and the reference
// fact answers empty for every authoring form the rules do not govern, so the rules
// decline rather than judging them.
func TestWorkloadImportRules_AreNotInvokedOverAFieldCarryingNoLibraryReference(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		build func() *types.Recipe
	}{
		{
			name: "a workload supplied by a file of the workspace",
			build: func() *types.Recipe {
				return workloadImportRecipe("./workloads/deployment.yaml")
			},
		},
		{
			name: "a workload written inline",
			build: func() *types.Recipe {
				r := minimalRecipe("workload-recipe")
				r.Spec.Workload = types.WorkloadTemplate{Inline: workloadImportBody}
				return r
			},
		},
		{
			name: "a values mapping supplied by a library import, which this axis presents and no rule here judges",
			build: func() *types.Recipe {
				r := minimalRecipe("workload-recipe")
				r.Spec.Source = "./chart"
				r.Spec.ValuesMapping = types.ValuesMapping{Import: "Absent#/mappings/Missing"}
				return r
			},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := declaredWorkloadHalt(t, tc.build(), workloadImportLibraries()); err != nil {
				t.Errorf("a rule judged a field it does not govern: %v", err)
			}
		})
	}
}

// TestWorkloadImportRules_ReportEveryOffendingNode measures the breadth the declaration
// adds: the render resolves one node at a time and stops at the first that fails, while
// the rules are invoked once per node and report all of them, each at its own position.
//
// # Why the breadth is read from the diagnostics beneath the halt
//
// Several findings come back as an envelope over one diagnostic per finding, and the
// envelope's own single-line rendering is its category and the wording the rule supplied
// — never the findings underneath. That is deliberate: a rendering is re-wrapped on its
// way up, so a multi-line one would carry its layout into every frame that wraps it, and
// the multi-line shape belongs to the surface writing to a terminal. An assertion over
// the envelope's line can therefore only ever see the wording this file's own caller
// passed in, whatever the rules found — it would report a rule that reported nothing
// exactly as it reports a rule that reported everything.
//
// The single-finding case hides this, because one objection is returned as itself rather
// than as a set of one: the position proof above reads a rendering straight off a
// one-finding halt for that reason, and the two are not the same read.
//
// The per-finding pairing below is the point of the walk rather than a stricter spelling
// of it. One diagnostic listing every offender would satisfy a search for the same names
// across the whole text while carrying a position an author can navigate to for none of
// them, which is the thing the declaration exists to add.
func TestWorkloadImportRules_ReportEveryOffendingNode(t *testing.T) {
	t.Parallel()

	first := workloadImportRecipe("Base#/workloads/MissingOne")
	first.Metadata.Name = "first-recipe"
	second := workloadImportRecipe("Base#/workloads/MissingTwo")
	second.Metadata.Name = "second-recipe"

	recipes := map[string]*types.Recipe{"api": first, "web": second}
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{})
	nodes.Set("web", types.IRNode{})
	graph := &types.IRGraph{Spec: types.IRSpec{Nodes: nodes}}

	view := newDescribedView(
		context.Background(), graph, stubGraphFacts{},
		recipes, RecipeLookup{}, nil, newStepDescriptorMemo(), nil, nil,
		nil, nil, nil, workloadImportLibraries(),
	)
	if view == nil {
		t.Fatal("the view was not built")
	}
	axes := newDescribedStageCoordinates(
		graph, localsAxis{nodes: []string{"api", "web"}}, recipes, RecipeLookup{}, nil)
	if axes == nil {
		t.Fatal("the enumeration refused to build")
	}

	err := runWorkloadImportRule(context.Background(), view, axes)
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
			if strings.Contains(leaf, want.recipe+" at .spec.workload") &&
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
	if text := err.Error(); !strings.Contains(text, "workload imports the compiler cannot resolve") {
		t.Errorf("the findings are not grouped under the rule's own lead: %s", text)
	}
}
