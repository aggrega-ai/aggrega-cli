// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// artifactCorpus assembles a render over nodeCount nodes, each declaring
// artifactsPerNode inline artifacts whose bodies read the node's own locals — so
// every value this stage's retention carries has something to hold for every
// node, and the bytes retained are evaluated bytes rather than the authored text.
//
// The bodies are deliberately document-sized rather than one line: the cost this
// retention is measured for is evaluated bytes per artifact, and a corpus of
// one-line entries would measure the map overhead instead of the thing that
// grows.
func artifactCorpus(nodeCount, artifactsPerNode int) (RendererInput, RecipeLookup, EngineFactory) {
	recipes := map[string]*types.Recipe{}
	for i := 0; i < nodeCount; i++ {
		nodeID := fmt.Sprintf("svc-%03d", i)

		locals := types.OrderedMap[string]{}
		locals.Set("release", fmt.Sprintf("r-%03d", i))
		locals.Set("slug", "{{ .context.instance_id }}")

		artifacts := types.OrderedMap[types.ArtifactEntry]{}
		for a := 0; a < artifactsPerNode; a++ {
			artifacts.Set(fmt.Sprintf("%s-%02d.yaml", nodeID, a), types.ArtifactEntry{
				Inline: fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %s-%02d
  labels:
    slug: "{{ .locals.slug }}"
    release: "{{ .locals.release }}"
data:
  index: "%02d"
  slug: "{{ .locals.slug }}"
  release: "{{ .locals.release }}"
  filler: "%s"
`, nodeID, a, a, strings.Repeat("padding-", 16)),
			})
		}

		recipes[nodeID] = &types.Recipe{
			APIVersion: "aggrega.ai/v1beta1",
			Kind:       "Recipe",
			Metadata:   types.RecipeMetadata{Name: fmt.Sprintf("recipe-%03d", i), Version: "1.0.0"},
			Spec: types.RecipeSpec{
				Capability: "compose",
				Locals:     locals,
				Artifacts:  artifacts,
			},
		}
	}
	return corpusFromRecipes(recipes)
}

// TestArtifactRetentionRecordsEveryProjectedArtifact asserts the retention covers
// the whole subject set, with no condition deciding which artifacts are worth
// retaining. A condition here would fail silently — an artifact left out simply
// has no answer, and nothing observes the absence.
func TestArtifactRetentionRecordsEveryProjectedArtifact(t *testing.T) {
	t.Parallel()

	const nodeCount = 12
	const artifactsPerNode = 4

	input, lookup, factory := artifactCorpus(nodeCount, artifactsPerNode)
	res, err := RenderAll(context.Background(), input, lookup, factory)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	if got := res.artifacts.recordedNodes(); got != nodeCount {
		t.Fatalf("retained %d nodes, want %d", got, nodeCount)
	}

	view := presentationOverTheRender(t, res)
	total := 0
	for _, nodeID := range input.Graph.Spec.Nodes.Keys() {
		names := view.ArtifactProjectionNameSet(nodeID)
		if len(names) != artifactsPerNode {
			t.Errorf("node %q projected %d artifacts, want %d", nodeID, len(names), artifactsPerNode)
			continue
		}
		for _, name := range names {
			content := view.ArtifactProjectionContent(nodeID, name)
			if len(content) == 0 {
				t.Errorf("node %q artifact %q answers with no bytes", nodeID, name)
				continue
			}
			// The bytes are the evaluated ones: the authored body carries template
			// references, so a retention on the wrong side of evaluation would still
			// answer with a plausible document.
			if strings.Contains(string(content), "{{") {
				t.Errorf("node %q artifact %q answers with unevaluated text", nodeID, name)
			}
			total++
		}
	}
	if want := nodeCount * artifactsPerNode; total != want {
		t.Fatalf("read %d artifacts through the view, want %d", total, want)
	}
	t.Logf("retention covers %d artifact(s) across %d node(s)", total, nodeCount)
}

// TestArtifactRetentionLeavesTheProjectionHandedToTheProviderUntouched is the
// before-and-after comparison at the point the retention was added: the producer
// runs twice over the same input, once carrying no retention and once carrying a
// live one, and what it hands the provider must be the same bytes either way.
//
// This is the narrow comparison rather than the render-wide one because the
// producer's output IS what reaches the provider — everything downstream consumes
// the projection and nothing consumes the retention — so byte-identity here is
// byte-identity of the emitted result, established without having to remove the
// retention from a shipped render to observe it.
func TestArtifactRetentionLeavesTheProjectionHandedToTheProviderUntouched(t *testing.T) {
	t.Parallel()

	bodies := map[string]string{
		"inline.yaml":  "kind: Inline\ninstance: {{ .context.instance_id }}\n",
		"second.yaml":  "kind: Second\nvalue: fixed\n",
		"guarded.yaml": "{{ if .values.absent }}kind: Guarded{{ end }}\n",
	}
	order := []string{"inline.yaml", "second.yaml", "guarded.yaml"}

	project := func(retention *artifactProjections) ArtifactsProjection {
		tmplCtx, state, nodeCtx := newArtifactsCtx()
		state.artifacts = retention
		resetArtifacts(state)
		for _, name := range order {
			state.Recipe.Spec.Artifacts.Set(name, types.ArtifactEntry{Inline: bodies[name]})
		}
		proj, err := buildArtifactsProjection(nil, tmplCtx, state, nodeCtx, "{{", "}}")
		if err != nil {
			t.Fatalf("buildArtifactsProjection: %v", err)
		}
		return proj
	}

	before := project(nil)
	live := newArtifactProjections()
	after := project(live)

	// A comparison of two empty projections would pass while proving nothing, so
	// the projection's own content is asserted before the two are compared.
	if len(before.Entries) == 0 {
		t.Fatal("the producer projected no entry; the comparison below would be between two empty sets")
	}
	bytesProjected := 0
	for _, e := range before.Entries {
		bytesProjected += len(e.Content)
	}
	if bytesProjected == 0 {
		t.Fatal("the projected entries carry no bytes; the comparison below would compare nothing")
	}
	// And the retention must actually have recorded, or the run labelled "after"
	// is a second run of the "before" arm under another name.
	if live.recordedNodes() == 0 {
		t.Fatal("the live retention recorded nothing; the two arms are the same arm")
	}

	if len(before.Entries) != len(after.Entries) {
		t.Fatalf("the producer projected %d entries without a retention and %d with one", len(before.Entries), len(after.Entries))
	}
	for i := range before.Entries {
		b, a := before.Entries[i], after.Entries[i]
		if b.Name != a.Name || b.Origin != a.Origin || string(b.Content) != string(a.Content) {
			t.Errorf("entry %d differs with the retention installed:\n without %q/%q/%q\n with    %q/%q/%q",
				i, b.Name, b.Origin, b.Content, a.Name, a.Origin, a.Content)
		}
	}
	t.Logf("%d entries / %d bytes projected identically with and without the retention", len(before.Entries), bytesProjected)
}

// TestArtifactRetentionLeavesEmittedBytesUntouched renders one corpus twice and
// requires the emitted bytes to agree, with the file count asserted first so a
// comparison of two empty sets cannot stand in for the comparison this makes —
// and with the digest itself calibrated, so a digest that could not tell two
// different outputs apart cannot report agreement.
func TestArtifactRetentionLeavesEmittedBytesUntouched(t *testing.T) {
	t.Parallel()

	const nodeCount = 30
	const artifactsPerNode = 4
	const wantFiles = nodeCount * artifactsPerNode

	input, lookup, factory := artifactCorpus(nodeCount, artifactsPerNode)

	first, err := RenderAll(context.Background(), input, lookup, factory)
	if err != nil {
		t.Fatalf("RenderAll (first): %v", err)
	}
	second, err := RenderAll(context.Background(), input, lookup, factory)
	if err != nil {
		t.Fatalf("RenderAll (second): %v", err)
	}

	firstDigest, firstCount := emittedDigest(first)
	secondDigest, secondCount := emittedDigest(second)

	if firstCount != wantFiles {
		t.Fatalf("emitted %d files, want %d; the comparison below would otherwise be over an empty or partial set", firstCount, wantFiles)
	}
	if secondCount != wantFiles {
		t.Fatalf("second render emitted %d files, want %d", secondCount, wantFiles)
	}
	if firstDigest != secondDigest {
		t.Errorf("emitted bytes differ across two renders of one input:\n first  %s\n second %s", firstDigest, secondDigest)
	}

	// Both renders must have retained, or the agreement says nothing about a
	// render that retains.
	if got := first.artifacts.recordedNodes(); got != nodeCount {
		t.Errorf("first render retained %d nodes, want %d", got, nodeCount)
	}
	if got := second.artifacts.recordedNodes(); got != nodeCount {
		t.Errorf("second render retained %d nodes, want %d", got, nodeCount)
	}

	// Calibration: one changed artifact body must move the digest. Without this, a
	// digest that folded every input to one value would report agreement above and
	// prove nothing about the bytes.
	altered, alteredLookup, alteredFactory := artifactCorpus(nodeCount, artifactsPerNode)
	recipe, found := alteredLookup.Recipe("svc-000", "")
	if !found {
		t.Fatal("the corpus binds no Recipe for the node the calibration alters")
	}
	name := recipe.Spec.Artifacts.Keys()[0]
	entry, _ := recipe.Spec.Artifacts.Get(name)
	entry.Inline += "  needle: \"planted\"\n"
	recipe.Spec.Artifacts.Set(name, entry)

	alteredRes, err := RenderAll(context.Background(), altered, alteredLookup, alteredFactory)
	if err != nil {
		t.Fatalf("RenderAll (altered): %v", err)
	}
	alteredDigest, alteredCount := emittedDigest(alteredRes)
	if alteredCount != wantFiles {
		t.Fatalf("the altered render emitted %d files, want %d", alteredCount, wantFiles)
	}
	if alteredDigest == firstDigest {
		t.Fatalf("the digest did not move for a changed artifact body; the agreement above is unproven")
	}
	t.Logf("%d files agreed across two renders; the digest moved for one changed body", firstCount)
}

// rewriteMarker is the prefix the rewriting emitter below puts in front of each
// artifact's bytes, which is what makes the file it emits a different buffer from
// the one the projection carried.
const rewriteMarker = "# rewritten by the provider\n"

// rewritingArtifactsEmitter emits each artifact into a NEW buffer rather than
// handing back the bytes the projection carried.
//
// It exists to measure the retention's cost in the case where its bytes are not
// already alive elsewhere. A provider that passes the projected bytes straight
// through leaves the retained slice aliasing the very buffer the result's own file
// map holds, so releasing the retention frees the bookkeeping and none of the
// bytes; a provider that transforms them leaves the retained slice as the only
// reference to it. Both are real provider behaviours, and they cost differently
// enough that reporting one alone would understate or overstate the retention.
type rewritingArtifactsEmitter struct {
	stubArtifactsEmitter
}

func (e *rewritingArtifactsEmitter) EmitArtifacts(
	_ context.Context, proj ArtifactsProjection, _ NodeRenderContext,
) ([]EmittedFile, error) {
	files := make([]EmittedFile, 0, len(proj.Entries))
	for _, ent := range proj.Entries {
		body := make([]byte, 0, len(rewriteMarker)+len(ent.Content))
		body = append(body, rewriteMarker...)
		body = append(body, ent.Content...)
		files = append(files, EmittedFile{Name: ent.Name, Origin: EmitOriginArtifactsInline, Body: body})
	}
	return files, nil
}

// artifactRetentionCost renders a corpus, lets plant add anything extra to the
// retained set, and reports the live heap with the retention held and again once
// it is released — everything else the render produced staying alive across both
// readings, so the difference is this retention's own cost and not the render's.
//
// Both references to the retention are dropped, not one: the presentation reads it
// by pointer, so releasing the set alone would leave it reachable through the view
// and the reading would report no cost for a retention that is still held.
func artifactRetentionCost(
	t *testing.T, nodeCount, artifactsPerNode int, plant func(*artifactProjections),
) (heldBytes, releasedBytes, heldObjects, releasedObjects uint64) {
	t.Helper()

	input, lookup, factory := artifactCorpus(nodeCount, artifactsPerNode)
	res, err := RenderAll(context.Background(), input, lookup, factory)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	if got := res.artifacts.recordedNodes(); got != nodeCount {
		t.Fatalf("retained %d nodes, want %d; measuring an unpopulated set proves nothing", got, nodeCount)
	}
	if plant != nil {
		plant(res.artifacts)
	}

	heldBytes, heldObjects = heapAfterCollection()
	res.artifacts = nil
	res.RenderedView = nil
	releasedBytes, releasedObjects = heapAfterCollection()
	runtime.KeepAlive(res)
	return heldBytes, releasedBytes, heldObjects, releasedObjects
}

// TestArtifactRetentionMemoryCost measures what this retention costs on a build
// with many nodes and many artifacts, and calibrates the measurement against a
// planted retention, so a reading of "no difference" cannot come from an
// instrument that reports none.
//
// It measures the LIVE heap either side of releasing the retained set rather than
// allocation volume: the claim under measurement is what stays reachable for the
// whole render, and a rate says nothing about that.
func TestArtifactRetentionMemoryCost(t *testing.T) {
	// Deliberately not parallel: it reads process-wide heap statistics, which a
	// concurrently allocating test would move underneath it.

	const nodeCount = 400
	const artifactsPerNode = 6

	held, released, heldObjs, releasedObjs := artifactRetentionCost(t, nodeCount, artifactsPerNode, nil)
	plainCost := int64(held) - int64(released)
	artifacts := int64(nodeCount * artifactsPerNode)
	t.Logf("retention over %d nodes x %d artifacts (%d artifacts): held heap %d bytes / %d objects; released heap %d bytes / %d objects; cost %d bytes (%d bytes per artifact)",
		nodeCount, artifactsPerNode, artifacts, held, heldObjs, released, releasedObjs, plainCost, plainCost/artifacts)

	// Calibration: plant a known quantity of retained bytes and require the same
	// instrument to report it. Without this, a cost near zero would be
	// indistinguishable from an instrument that measures nothing.
	const plantedPerNode = 8 << 10
	plantedTotal := int64(nodeCount) * plantedPerNode
	needleHeld, needleReleased, _, _ := artifactRetentionCost(t, nodeCount, artifactsPerNode, func(a *artifactProjections) {
		for i := 0; i < nodeCount; i++ {
			a.record(fmt.Sprintf("svc-%03d", i), "planted.yaml", []byte(strings.Repeat("x", plantedPerNode)))
		}
	})
	needleCost := int64(needleHeld) - int64(needleReleased)
	t.Logf("calibration: planted %d bytes of retention; measured cost %d bytes (plain %d)", plantedTotal, needleCost, plainCost)

	if needleCost-plainCost < plantedTotal/2 {
		t.Fatalf("the instrument did not report a planted retention: planted %d bytes, measured cost moved from %d to %d; every other reading here is unproven",
			plantedTotal, plainCost, needleCost)
	}

	// The reading above is the aliasing case: the provider handed the projected
	// bytes straight back, so the result's own file map holds the very buffers the
	// retention points at and releasing the retention frees the bookkeeping alone.
	// This is the other case, where the provider emits a transformed buffer and the
	// retention is the only thing holding the evaluated bytes. Both are reported
	// because a provider does one or the other and the two cost differently.
	input, lookup, _ := artifactCorpus(nodeCount, artifactsPerNode)
	rewriting := func(_ string) (Provider, error) { return &rewritingArtifactsEmitter{}, nil }
	res, err := RenderAll(context.Background(), input, lookup, rewriting)
	if err != nil {
		t.Fatalf("RenderAll (rewriting provider): %v", err)
	}
	if got := res.artifacts.recordedNodes(); got != nodeCount {
		t.Fatalf("the rewriting render retained %d nodes, want %d", got, nodeCount)
	}
	// The emitted bytes must genuinely be a different buffer, or this reading is
	// the aliasing one under another name.
	var sampled []byte
	for _, af := range res.Files {
		sampled = af.Content
		break
	}
	if !strings.HasPrefix(string(sampled), rewriteMarker) {
		t.Fatalf("the rewriting provider's output does not carry its marker; this reading is the aliasing case under another name")
	}
	unaliasedHeld, unaliasedHeldObjs := heapAfterCollection()
	res.artifacts = nil
	res.RenderedView = nil
	unaliasedReleased, unaliasedReleasedObjs := heapAfterCollection()
	runtime.KeepAlive(res)
	unaliasedCost := int64(unaliasedHeld) - int64(unaliasedReleased)
	t.Logf("unaliased retention over %d artifacts (provider emitting its own buffers): held heap %d bytes / %d objects; released heap %d bytes / %d objects; cost %d bytes (%d bytes per artifact)",
		artifacts, unaliasedHeld, unaliasedHeldObjs, unaliasedReleased, unaliasedReleasedObjs, unaliasedCost, unaliasedCost/artifacts)
}
