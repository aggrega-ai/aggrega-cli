// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"reflect"
	"slices"
	"sort"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// declaredAtRenderedStage returns the accessor names the framework declares at
// the rendered stage, derived from the generated views rather than listed here.
//
// The derivation is the difference between the stage's own view and the view it
// extends: a stage's view embeds the one before it, so every name present in the
// first and absent from the second is declared AT this stage. A name added at an
// earlier stage therefore does not perturb this, and a name added at this stage
// appears here with no edit.
func declaredAtRenderedStage(t *testing.T) []string {
	t.Helper()
	rendered := reflect.TypeOf((*constraint.RenderedView)(nil)).Elem()
	hydrated := reflect.TypeOf((*constraint.HydratedView)(nil)).Elem()

	inherited := map[string]bool{}
	for i := 0; i < hydrated.NumMethod(); i++ {
		inherited[hydrated.Method(i).Name] = true
	}
	var own []string
	for i := 0; i < rendered.NumMethod(); i++ {
		if name := rendered.Method(i).Name; !inherited[name] {
			own = append(own, name)
		}
	}
	sort.Strings(own)
	return own
}

// retainedArtifactFieldNames returns the names this stage's own retention
// carries, taken from the two structures that hold them.
//
// Both are read for the reason the mid-render retention's own guard reads two:
// one structure holds what is retained per node and the other what is retained
// per render, and a fact answered from either is held.
func retainedArtifactFieldNames(t *testing.T) []string {
	t.Helper()
	var names []string
	for _, typ := range []reflect.Type{
		reflect.TypeOf(artifactProjection{}),
		reflect.TypeOf(artifactProjections{}),
	} {
		for i := 0; i < typ.NumField(); i++ {
			if f := typ.Field(i); f.IsExported() {
				names = append(names, f.Name)
			}
		}
	}
	sort.Strings(names)
	return names
}

// delegatedAtRenderedStage returns the accessor names the presentation answers
// from what the render ALREADY retains, derived from the seam it delegates them
// through rather than from a list.
//
// Deriving them matters as much as deriving the retained set. Two of this stage's
// facts read the attribution the assembly retains beside each group, so they need
// no field of their own — and a hand-written note of which two would be a second
// copy of the split, going stale exactly when a further fact is declared here.
// The seam's own method set is the split.
func delegatedAtRenderedStage(t *testing.T) []string {
	t.Helper()
	seam := reflect.TypeOf((*assembledOutputFacts)(nil)).Elem()
	names := make([]string, 0, seam.NumMethod())
	for i := 0; i < seam.NumMethod(); i++ {
		names = append(names, seam.Method(i).Name)
	}
	sort.Strings(names)
	return names
}

// TestRetainedProjectionCoversEveryDeclaredRenderedFact holds this stage's two
// answering sources to the framework's declarations instead of to a list somebody
// maintains.
//
// A name declared at this stage with nothing to answer it from would leave the
// retention silently behind — the declaration would exist, the presentation would
// return a zero value, and no build would fail. So the sets are compared in every
// direction and the failure names the offending side.
func TestRetainedProjectionCoversEveryDeclaredRenderedFact(t *testing.T) {
	t.Parallel()

	declared := declaredAtRenderedStage(t)
	retained := retainedArtifactFieldNames(t)
	delegated := delegatedAtRenderedStage(t)

	// A comparison of empty sets would pass while proving nothing, so every set's
	// non-emptiness is asserted before the comparison rather than assumed.
	if len(declared) == 0 {
		t.Fatalf("no accessor is declared at this stage; the derivation from the generated views is broken, not the retention")
	}
	if len(retained) == 0 {
		t.Fatalf("this stage's retention carries no field at all")
	}
	if len(delegated) == 0 {
		t.Fatalf("the delegation seam declares no method; the split between retained and already-retained facts is not derivable")
	}

	answered := map[string]string{}
	for _, name := range retained {
		answered[name] = "the retention taken for this stage"
	}
	for _, name := range delegated {
		if from, clash := answered[name]; clash {
			t.Errorf("%q is answered both by %s and by the delegation seam; one fact has one producer", name, from)
			continue
		}
		answered[name] = "the delegation seam"
	}

	for _, name := range declared {
		if _, ok := answered[name]; !ok {
			t.Errorf("nothing answers %q: it is declared at this stage, no field retains it and the delegation seam does not carry it", name)
		}
	}

	declaredSet := map[string]bool{}
	for _, name := range declared {
		declaredSet[name] = true
	}
	for _, name := range retained {
		if !declaredSet[name] {
			t.Errorf("the retention carries %q, which is declared at no stage past the one before this; retain what the declarations name and nothing else", name)
		}
	}
	for _, name := range delegated {
		if !declaredSet[name] {
			t.Errorf("the delegation seam carries %q, which is declared at no stage past the one before this; a seam method left behind by a withdrawn fact answers a question nobody asks", name)
		}
	}

	t.Logf("declared at this stage %v; retained %v; delegated %v", declared, retained, delegated)
}

// whitespaceArtifactRecipe returns a Recipe declaring three artifacts, the middle
// one guarded so that it evaluates to nothing.
//
// The guard is a condition that is false rather than an entry authored as blank
// text, because that is the shape a Recipe actually takes: an author writes a
// presence-guarded body and the entry contributes a file only for the instances
// that satisfy the guard.
func whitespaceArtifactRecipe(name string) *types.Recipe {
	artifacts := types.OrderedMap[types.ArtifactEntry]{}
	artifacts.Set("first.yaml", types.ArtifactEntry{Inline: "kind: First\n"})
	artifacts.Set("guarded.yaml", types.ArtifactEntry{
		Inline: "{{ if .values.enable_guarded }}kind: Guarded{{ end }}\n",
	})
	artifacts.Set("last.yaml", types.ArtifactEntry{Inline: "kind: Last\n"})

	recipe := sourceRecipe(name, nil)
	recipe.Spec.Artifacts = artifacts
	return recipe
}

// TestRetainedArtifactKeySetIsTheRendersNotTheRecipes is the divergence proof.
//
// The text standing at each entry is recorded before evaluation; the evaluated
// bytes are recorded after the walk has dropped an entry that evaluated to
// nothing. So the two key sets differ on an input carrying such an entry, and
// this asserts the difference in the direction that matters: the entry is in the
// earlier key set and absent from this one. A corpus where the two coincide would
// pass with a retention taken at the wrong point still in place.
func TestRetainedArtifactKeySetIsTheRendersNotTheRecipes(t *testing.T) {
	t.Parallel()

	const node = "diverges"
	recipe := whitespaceArtifactRecipe(node)
	input, lookup, factory := corpusFromRecipes(map[string]*types.Recipe{node: recipe})

	res, err := RenderAll(context.Background(), input, lookup, factory)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	declaredNames := recipe.Spec.Artifacts.Keys()
	if len(declaredNames) != 3 {
		t.Fatalf("the fixture declares %d artifacts, want 3", len(declaredNames))
	}

	// The earlier key set: the text standing at every entry the Recipe declares,
	// the guarded one included. If this did not hold all three, the divergence
	// below would be a property of the fixture rather than of the two recording
	// positions.
	hydrated, ok := res.hydrated.projection(node)
	if !ok {
		t.Fatalf("the node has no retained mid-render projection")
	}
	if got := hydrated.ArtifactTemplateText.Keys(); !slices.Equal(got, declaredNames) {
		t.Fatalf("the pre-evaluation key set is %v, want every declared entry %v; the comparison below would not be between two positions", got, declaredNames)
	}

	// This stage's key set: the entry that evaluated to nothing is gone.
	projected, ok := res.artifacts.projection(node)
	if !ok {
		t.Fatalf("the node has no retained artifact projection")
	}
	want := []string{"first.yaml", "last.yaml"}
	if got := projected.ArtifactProjectionNameSet; !slices.Equal(got, want) {
		t.Errorf("the projected key set is %v, want %v", got, want)
	}
	if _, held := projected.ArtifactProjectionContent["guarded.yaml"]; held {
		t.Errorf("the projection holds content for an entry that evaluated to nothing")
	}

	// And the two sets are named as different, so a future change collapsing the
	// two recording positions fails here rather than passing quietly.
	if slices.Equal(hydrated.ArtifactTemplateText.Keys(), projected.ArtifactProjectionNameSet) {
		t.Errorf("the two key sets coincide; the evaluated retention is being taken at the pre-evaluation position")
	}

	// The presentation answers the same way, which is what a rule reads.
	view := presentationOverTheRender(t, res)
	if got := view.ArtifactProjectionNameSet(node); !slices.Equal(got, want) {
		t.Errorf("ArtifactProjectionNameSet(%q) = %v, want %v", node, got, want)
	}
	if got := view.ArtifactProjectionContent(node, "guarded.yaml"); got != nil {
		t.Errorf("ArtifactProjectionContent(%q, \"guarded.yaml\") = %q, want no bytes", node, got)
	}
	if got := string(view.ArtifactProjectionContent(node, "first.yaml")); got != "kind: First\n" {
		t.Errorf("ArtifactProjectionContent(%q, \"first.yaml\") = %q", node, got)
	}
}

// TestRetainedArtifactBytesAreTheEvaluatedOnes pins the recording side of
// evaluation: what is retained is the bytes the entry evaluated to, not the text
// standing at it. The fixture's entries carry template references, so a retention
// taken before evaluation would still be a plausible string.
func TestRetainedArtifactBytesAreTheEvaluatedOnes(t *testing.T) {
	t.Parallel()

	const node = "evaluates"
	artifacts := types.OrderedMap[types.ArtifactEntry]{}
	artifacts.Set("out.yaml", types.ArtifactEntry{Inline: "instance: {{ .context.instance_id }}\n"})

	recipe := sourceRecipe(node, nil)
	recipe.Spec.Artifacts = artifacts

	input, lookup, factory := corpusFromRecipes(map[string]*types.Recipe{node: recipe})
	res, err := RenderAll(context.Background(), input, lookup, factory)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	authored, _ := recipe.Spec.Artifacts.Get("out.yaml")
	got := string(presentationOverTheRender(t, res).ArtifactProjectionContent(node, "out.yaml"))
	if got == authored.Inline {
		t.Fatalf("the retained bytes are the authored text %q; the retention is on the wrong side of evaluation", got)
	}
	if want := "instance: " + node + "\n"; got != want {
		t.Errorf("retained bytes %q, want the evaluated %q", got, want)
	}
}

// TestArtifactProjectionIsRetainedOnlyForANodesOwnRecipe holds the discriminator
// that keeps an attachment's artifacts out of a value declared over a node's own
// Recipe. Both renders walk the same producer under one node identity; only the
// one carrying the render's retention records anything.
func TestArtifactProjectionIsRetainedOnlyForANodesOwnRecipe(t *testing.T) {
	t.Parallel()

	store := newArtifactProjections()

	build := func(attach *artifactProjections, text string) {
		tmplCtx, state, nodeCtx := newArtifactsCtx()
		state.artifacts = attach
		resetArtifacts(state)
		state.Recipe.Spec.Artifacts.Set("out.yaml", types.ArtifactEntry{Inline: text})
		if _, err := buildArtifactsProjection(nil, tmplCtx, state, nodeCtx, "{{", "}}"); err != nil {
			t.Fatalf("buildArtifactsProjection: %v", err)
		}
	}

	build(nil, "kind: FromAnAttachment\n")
	if store.recordedNodes() != 0 {
		t.Fatalf("a render carrying no retention recorded %d nodes", store.recordedNodes())
	}

	build(store, "kind: FromTheNodesOwnRecipe\n")
	p, ok := store.projection("api")
	if !ok {
		t.Fatalf("the node's own render recorded nothing")
	}
	if got := len(p.ArtifactProjectionNameSet); got != 1 {
		t.Fatalf("retained %d artifacts, want exactly the node's own", got)
	}
	if got := string(p.ArtifactProjectionContent["out.yaml"]); got != "kind: FromTheNodesOwnRecipe\n" {
		t.Errorf("retained %q", got)
	}
}

// TestRenderedPresentationRefusesAPartialConstruction asserts the presentation is
// absent rather than partial when a source it reads is missing. An accessor
// returning a plausible zero value is silent where an absent presentation is
// loud, so the constructor refuses instead of answering a whole class of facts
// from nothing.
func TestRenderedPresentationRefusesAPartialConstruction(t *testing.T) {
	t.Parallel()

	result := &RenderResult{}
	earlier := newHydratedView(nil, nil)
	live := newArtifactProjections()

	for _, tc := range []struct {
		name        string
		earlier     constraint.HydratedView
		assembled   assembledOutputFacts
		projections *artifactProjections
	}{
		{"no earlier presentation", nil, assembledOutput{result: result}, live},
		{"no assembled-output seam", stubHydratedView{}, nil, live},
		{"no artifact retention", stubHydratedView{}, assembledOutput{result: result}, nil},
	} {
		if got := newRenderedView(tc.earlier, tc.assembled, tc.projections); got != nil {
			t.Errorf("%s: a presentation was built anyway", tc.name)
		}
	}
	if earlier != nil {
		t.Fatal("the earlier presentation refuses its own partial construction; this fixture assumes it does")
	}
}

// stubHydratedView stands in for the earlier presentation where only this stage's
// own accessors are under test. It answers nothing, so reading an earlier fact
// through it is a nil dereference rather than a wrong answer — which is the point:
// a test that reached for one would be reaching past what this package can build.
type stubHydratedView struct {
	constraint.HydratedView
}

// presentationOverTheRender builds this stage's presentation over the retention a
// real render filled, with the earlier presentation stood in for.
//
// The stand-in is structural rather than a convenience. This stage's presentation
// embeds the presentation of the state each node reached before its bytes existed,
// which in turn descends from the finalised graph's own presentation — and that
// chain is constructible only by a whole compilation, because the stage below the
// graph exposes no constructor outside its own package. This package renders a
// graph rather than compiling one, so the presentation a render publishes is
// nil here by construction.
//
// What that costs and what it does not: the two accessors this stage adds read the
// retention a real render produced, so they are exercised against real values
// here. The presentation the render itself publishes — every accessor answering,
// the earlier stage delegated to, a rule invoked through it — is exercised where a
// compilation is available.
func presentationOverTheRender(t *testing.T, res *RenderResult) constraint.RenderedView {
	t.Helper()
	view := newRenderedView(stubHydratedView{}, assembledOutput{result: res}, res.artifacts)
	if view == nil {
		t.Fatal("the presentation refused to build over a completed render")
	}
	return view
}
