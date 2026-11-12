// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// declaredAtHydratedStage returns the accessor names the framework declares at
// the hydrated stage, derived from the generated views rather than listed here.
//
// The derivation is the difference between the stage's own view and the view it
// extends: a stage's view embeds the one before it, so every name present in the
// first and absent from the second is declared AT the hydrated stage. A name
// added at an earlier stage therefore does not perturb this, and a name added at
// this stage appears here with no edit.
func declaredAtHydratedStage(t *testing.T) []string {
	t.Helper()
	hydrated := reflect.TypeOf((*constraint.HydratedView)(nil)).Elem()
	described := reflect.TypeOf((*constraint.DescribedView)(nil)).Elem()

	inherited := map[string]bool{}
	for i := 0; i < described.NumMethod(); i++ {
		inherited[described.Method(i).Name] = true
	}
	var own []string
	for i := 0; i < hydrated.NumMethod(); i++ {
		if name := hydrated.Method(i).Name; !inherited[name] {
			own = append(own, name)
		}
	}
	sort.Strings(own)
	return own
}

// projectedFieldNames returns the names the retention carries, taken from every
// structure that holds one.
//
// All four are read because the retention is held at four scopes, and each scope
// is a different thing being retained rather than a different place to put the
// same thing. One value is held per render — the resolved provides, already
// evaluated once for the whole render, so the retention holds that bag itself
// rather than a copy under every node. The rest are held per node, per attachment
// on a node, and per boundary-scope attachment: three documents author the source,
// the workload and the artifacts a render reads, and an attachment's render runs
// under the host component's node identity, so a single per-node scope would
// answer the host's values for the attachment's documents.
//
// A structure added here later that holds no declared name fails the comparison
// below from its other direction, so this list cannot be widened into one that
// admits an unnamed field.
func projectedFieldNames(t *testing.T) []string {
	t.Helper()
	var names []string
	for _, typ := range []reflect.Type{
		reflect.TypeOf(hydratedProjection{}),
		reflect.TypeOf(hydratedProjections{}),
		reflect.TypeOf(nodeAttachmentProjection{}),
		reflect.TypeOf(boundaryAttachmentProjection{}),
	} {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if !f.IsExported() {
				continue
			}
			names = append(names, f.Name)
		}
	}
	sort.Strings(names)
	return names
}

// TestRetainedProjectionCoversEveryDeclaredHydratedFact holds the retention to
// the framework's declarations instead of to a list somebody maintains.
//
// A name declared at this stage with no field to hold it would leave the
// retention silently behind — the declaration would exist, nothing would carry
// its value, and no build would fail. So the two sets are compared in both
// directions and the failure names the offending side.
func TestRetainedProjectionCoversEveryDeclaredHydratedFact(t *testing.T) {
	t.Parallel()

	declared := declaredAtHydratedStage(t)
	projected := projectedFieldNames(t)

	// A comparison of two empty sets would pass while proving nothing, so the
	// non-emptiness is asserted before the comparison rather than assumed.
	if len(declared) == 0 {
		t.Fatalf("no accessor is declared at this stage; the derivation from the generated views is broken, not the retention")
	}
	if len(projected) == 0 {
		t.Fatalf("the retention carries no field at all")
	}

	held := map[string]bool{}
	for _, name := range projected {
		held[name] = true
	}
	for _, name := range declared {
		if !held[name] {
			t.Errorf("nothing retains %q: it is declared at this stage and no field carries it", name)
		}
	}

	declaredSet := map[string]bool{}
	for _, name := range declared {
		declaredSet[name] = true
	}
	for _, name := range projected {
		if !declaredSet[name] {
			t.Errorf("the retention carries %q, which is declared at no stage past the one before this; retain what the declarations name and nothing else", name)
		}
	}
}

// TestRetainedProjectionRecordsEveryNodeUnconditionally asserts the retention is
// taken for every node, with no condition deciding which nodes are worth it. A
// condition here would fail silently — a node left out simply has no answer, and
// nothing observes the absence.
func TestRetainedProjectionRecordsEveryNodeUnconditionally(t *testing.T) {
	t.Parallel()

	const nodeCount = 12
	input, lookup, factory := hydrationCorpus(nodeCount)
	res, err := RenderAll(context.Background(), input, lookup, factory)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	if got := res.hydrated.recordedNodes(); got != nodeCount {
		t.Fatalf("recorded %d nodes, want %d", got, nodeCount)
	}
	for _, nodeID := range input.Graph.Spec.Nodes.Keys() {
		if _, ok := res.hydrated.projection(nodeID); !ok {
			t.Errorf("node %q has no retained projection", nodeID)
		}
	}
}

// TestRetainedProjectionRecordsTheResolvedSource pins the recording position:
// the value taken is the source AFTER resolution, not the text the author wrote.
// A source template evaluating to an empty string leaves the node with no
// source, and a recording taken before resolution would report one.
func TestRetainedProjectionRecordsTheResolvedSource(t *testing.T) {
	t.Parallel()

	recipes := map[string]*types.Recipe{
		"resolves-empty": sourceRecipe("resolves-empty", "{{ .locals.blank }}"),
		"resolves-full":  sourceRecipe("resolves-full", "./chart"),
		"declares-none":  sourceRecipe("declares-none", nil),
	}
	input, lookup, factory := corpusFromRecipes(recipes)
	res, err := RenderAll(context.Background(), input, lookup, factory)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	for _, tc := range []struct {
		nodeID string
		want   bool
	}{
		{"resolves-empty", false},
		{"resolves-full", true},
		{"declares-none", false},
	} {
		p, ok := res.hydrated.projection(tc.nodeID)
		if !ok {
			t.Fatalf("node %q has no retained projection", tc.nodeID)
		}
		if p.ResolvedNodeSourceDeclared != tc.want {
			t.Errorf("node %q: retained source-declared = %v, want %v", tc.nodeID, p.ResolvedNodeSourceDeclared, tc.want)
		}
	}
}

// TestRetainedProjectionRecordsResolvedArtifactText asserts the recorded text is
// the entry's resolved content rather than the directive that pointed at it, for
// the imported form as well as the inline one.
func TestRetainedProjectionRecordsResolvedArtifactText(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	imported := writeArtifactFile(t, dir, "imported.yaml", "kind: Imported\nname: from-a-file\n")

	artifacts := types.OrderedMap[types.ArtifactEntry]{}
	artifacts.Set("inline.yaml", types.ArtifactEntry{Inline: "kind: Inline\n"})
	artifacts.Set("imported.yaml", types.ArtifactEntry{Import: imported})

	recipe := sourceRecipe("mixed", nil)
	recipe.Spec.Artifacts = artifacts

	input, lookup, factory := corpusFromRecipes(map[string]*types.Recipe{"mixed": recipe})
	res, err := RenderAll(context.Background(), input, lookup, factory)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	p, ok := res.hydrated.projection("mixed")
	if !ok {
		t.Fatalf("node has no retained projection")
	}
	if got := p.ArtifactTemplateText.Len(); got != 2 {
		t.Fatalf("retained %d artifact texts, want 2", got)
	}
	if got, _ := p.ArtifactTemplateText.Get("inline.yaml"); got != "kind: Inline\n" {
		t.Errorf("inline entry retained %q", got)
	}
	got, _ := p.ArtifactTemplateText.Get("imported.yaml")
	if got != "kind: Imported\nname: from-a-file\n" {
		t.Errorf("imported entry retained %q, want the file's content rather than the directive", got)
	}
}

// TestArtifactTextIsRetainedOnlyForANodesOwnRecipe holds the discriminator that
// keeps an attachment's artifact templates out of a value declared over a node's
// own Recipe. Both renders walk the same artifacts path under one node identity;
// only the one carrying the render's retention records anything.
func TestArtifactTextIsRetainedOnlyForANodesOwnRecipe(t *testing.T) {
	t.Parallel()

	store := newHydratedProjections(NodeProvides{})

	build := func(attach *hydratedProjections, text string) {
		tmplCtx, state, nodeCtx := newArtifactsCtx()
		state.hydrated = attach
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
	if got := p.ArtifactTemplateText.Len(); got != 1 {
		t.Fatalf("retained %d artifact texts, want exactly the node's own", got)
	}
	if got, _ := p.ArtifactTemplateText.Get("out.yaml"); got != "kind: FromTheNodesOwnRecipe\n" {
		t.Errorf("retained %q", got)
	}
}
