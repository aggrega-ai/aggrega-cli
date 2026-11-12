// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// corpusFromRecipes assembles a single-boundary render over the given Recipes,
// one node per key, against a provider stub that emits each artifact entry at
// its own name.
func corpusFromRecipes(recipes map[string]*types.Recipe) (RendererInput, RecipeLookup, EngineFactory) {
	ids := make([]string, 0, len(recipes))
	for id := range recipes {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	nodes := types.OrderedMap[types.IRNode]{}
	for _, id := range ids {
		nodes.Set(id, types.IRNode{
			Component:       "core/compute/api-server",
			BoundaryPath:    "prod",
			ResolvedVersion: "1.0.0",
		})
	}

	input := RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "corpus"},
			Spec: types.IRSpec{
				Nodes:      nodes,
				Provenance: types.IRProvenance{CompilerVersion: "0.1.0", AggregateVersion: "1.0.0"},
			},
		},
		FeatureOrder: ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "corpus"}},
			Release:       &types.Release{Metadata: types.ReleaseMetadata{Name: "corpus-release", Environment: "test"}},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
	}
	lookup := RecipeLookup{
		Recipe: func(nodeID, _ string) (*types.Recipe, bool) {
			r, ok := recipes[nodeID]
			return r, ok
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}
	factory := func(_ string) (Provider, error) { return &stubArtifactsEmitter{}, nil }
	return input, lookup, factory
}

// sourceRecipe builds a minimal Recipe carrying the given source value and one
// empty local, which is what lets a source authored as a template resolve to an
// empty string.
func sourceRecipe(name string, source any) *types.Recipe {
	locals := types.OrderedMap[string]{}
	locals.Set("blank", "")
	return &types.Recipe{
		APIVersion: "aggrega.ai/v1beta1",
		Kind:       "Recipe",
		Metadata:   types.RecipeMetadata{Name: name, Version: "1.0.0"},
		Spec: types.RecipeSpec{
			Capability: "compose",
			Locals:     locals,
			Source:     source,
		},
	}
}

// writeArtifactFile writes an artifact fixture at name under dir and returns its
// absolute path. name may carry directory segments — the two files a
// same-base-name import proof needs differ only in the directory that holds them —
// so the intermediate directories are created rather than assumed.
func writeArtifactFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// hydrationCorpus assembles a many-node render whose nodes each declare locals,
// provides, an inline values mapping and two inline artifacts — so every value
// the retention carries has something to hold for every node.
func hydrationCorpus(nodeCount int) (RendererInput, RecipeLookup, EngineFactory) {
	recipes := map[string]*types.Recipe{}
	for i := 0; i < nodeCount; i++ {
		nodeID := fmt.Sprintf("svc-%03d", i)

		locals := types.OrderedMap[string]{}
		locals.Set("release", fmt.Sprintf("r-%03d", i))
		locals.Set("slug", "{{ .context.instance_id }}")
		locals.Set("tier", "{{ .locals.slug }}-tier")

		provides := types.OrderedMap[string]{}
		provides.Set("host", "{{ .locals.slug }}.svc")
		provides.Set("port", "8080")

		artifacts := types.OrderedMap[types.ArtifactEntry]{}
		artifacts.Set(nodeID+"-configmap.yaml", types.ArtifactEntry{Inline: fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %s-config
data:
  slug: "{{ .locals.slug }}"
  tier: "{{ .locals.tier }}"
`, nodeID)})
		artifacts.Set(nodeID+"-service.yaml", types.ArtifactEntry{Inline: fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: %s-svc
spec:
  ports:
    - port: 8080
`, nodeID)})

		recipes[nodeID] = &types.Recipe{
			APIVersion: "aggrega.ai/v1beta1",
			Kind:       "Recipe",
			Metadata:   types.RecipeMetadata{Name: fmt.Sprintf("recipe-%03d", i), Version: "1.0.0"},
			Spec: types.RecipeSpec{
				Capability: "compose",
				Locals:     locals,
				Provides:   provides,
				Artifacts:  artifacts,
				ValuesMapping: types.ValuesMapping{Inline: `image: "{{ .context.instance_id }}"
replicas: 1
`},
			},
		}
	}
	return corpusFromRecipes(recipes)
}

// emittedDigest reduces a render's emitted files to one digest over every path,
// its content and its declared format, read in path order.
func emittedDigest(res *RenderResult) (string, int) {
	paths := make([]string, 0, len(res.Files))
	for p := range res.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	total := sha256.New()
	for _, p := range paths {
		f := res.Files[p]
		sum := sha256.Sum256(f.Content)
		fmt.Fprintf(total, "%s\x00%s\x00%s\n", p, hex.EncodeToString(sum[:]), f.Format)
	}
	return hex.EncodeToString(total.Sum(nil)), len(paths)
}

// TestRetentionLeavesEmittedBytesUntouched renders one corpus twice and requires
// the emitted bytes to agree, with the file count asserted first so a comparison
// of two empty sets cannot stand in for the comparison this makes.
func TestRetentionLeavesEmittedBytesUntouched(t *testing.T) {
	t.Parallel()

	const nodeCount = 40
	const wantFiles = nodeCount * 2 // two artifacts per node

	input, lookup, factory := hydrationCorpus(nodeCount)

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

	// Both renders must have retained, or the comparison says nothing about a
	// render that retains.
	if got := first.hydrated.recordedNodes(); got != nodeCount {
		t.Errorf("first render retained %d nodes, want %d", got, nodeCount)
	}
	if got := second.hydrated.recordedNodes(); got != nodeCount {
		t.Errorf("second render retained %d nodes, want %d", got, nodeCount)
	}
}

// heapAfterCollection reports the live heap once collection has settled.
//
// Two collections rather than one: the first may queue finalizers whose subjects
// only become collectable on the pass after, so a single call reads a heap that
// is still shrinking.
func heapAfterCollection() (allocBytes, objects uint64) {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc, ms.HeapObjects
}

// retentionCost renders a corpus, lets plant add anything extra to the retained
// set, and reports the live heap with the retention held and again once it is
// released — everything else the render produced staying alive across both
// readings, so the difference is the retention's own cost and not the render's.
func retentionCost(t *testing.T, nodeCount int, plant func(*hydratedProjections)) (heldBytes, releasedBytes, heldObjects, releasedObjects uint64) {
	t.Helper()

	input, lookup, factory := hydrationCorpus(nodeCount)
	res, err := RenderAll(context.Background(), input, lookup, factory)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	if got := res.hydrated.recordedNodes(); got != nodeCount {
		t.Fatalf("retained %d nodes, want %d; measuring an unpopulated set proves nothing", got, nodeCount)
	}
	if plant != nil {
		plant(res.hydrated)
	}

	heldBytes, heldObjects = heapAfterCollection()
	res.hydrated = nil
	releasedBytes, releasedObjects = heapAfterCollection()
	runtime.KeepAlive(res)
	return heldBytes, releasedBytes, heldObjects, releasedObjects
}

// TestRetainedProjectionMemoryCost measures what the retention costs on a
// many-node build and calibrates the measurement against a planted retention, so
// a reading of "no difference" cannot come from an instrument that reports none.
//
// It measures the LIVE heap either side of releasing the retained set rather
// than allocation volume: the claim under measurement is what stays reachable
// for the whole render, and a rate says nothing about that.
func TestRetainedProjectionMemoryCost(t *testing.T) {
	// Deliberately not parallel: it reads process-wide heap statistics, which a
	// concurrently allocating test would move underneath it.

	const nodeCount = 500

	held, released, heldObjs, releasedObjs := retentionCost(t, nodeCount, nil)
	plainCost := int64(held) - int64(released)
	t.Logf("retention over %d nodes: held heap %d bytes / %d objects; released heap %d bytes / %d objects; cost %d bytes (%d bytes per node)",
		nodeCount, held, heldObjs, released, releasedObjs, plainCost, plainCost/int64(nodeCount))

	// Calibration: plant a known quantity of retained bytes and require the same
	// instrument to report it. Without this, a cost near zero would be
	// indistinguishable from an instrument that measures nothing.
	const plantedPerNode = 8 << 10
	plantedTotal := int64(nodeCount) * plantedPerNode
	needleHeld, needleReleased, _, _ := retentionCost(t, nodeCount, func(h *hydratedProjections) {
		for i := 0; i < nodeCount; i++ {
			h.recordArtifactTemplate(fmt.Sprintf("svc-%03d", i), "planted.yaml", strings.Repeat("x", plantedPerNode))
		}
	})
	needleCost := int64(needleHeld) - int64(needleReleased)
	t.Logf("calibration: planted %d bytes of retention; measured cost %d bytes (plain %d)", plantedTotal, needleCost, plainCost)

	if needleCost-plainCost < plantedTotal/2 {
		t.Fatalf("the instrument did not report a planted retention: planted %d bytes, measured cost moved from %d to %d; every other reading here is unproven",
			plantedTotal, plainCost, needleCost)
	}
}
