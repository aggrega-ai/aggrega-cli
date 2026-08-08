// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// collisionEngine is a synthetic provider engine that emits a fixed set of
// files for every node it renders. Two nodes rendered through it therefore emit
// the same paths, which is the shape a collision at assembly takes: independent
// contributors landing on one output path.
//
// The base type implements neither BoundaryAssembler nor BoundaryAssemblyReporter,
// so a dispatch group of these finalizes flat — per-node files land directly
// under the dispatch path with nothing to keep them apart.
type collisionEngine struct {
	emit []renderer.RenderedFile
}

func (e *collisionEngine) Render(_ context.Context, _ map[string]any, _ renderer.NodeRenderContext) ([]renderer.RenderedFile, error) {
	out := make([]renderer.RenderedFile, len(e.emit))
	copy(out, e.emit)
	return out, nil
}

func (e *collisionEngine) Contract() renderer.ProviderContract {
	return renderer.ProviderContract{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
}

// collisionAssemblerEngine adds boundary assembly to collisionEngine: it returns
// a fixed merged set and controls whether per-node files nest under the
// contributing instance. nest=true is the shape that keeps two contributors'
// identically-named files apart; nest=false is the shape that does not.
type collisionAssemblerEngine struct {
	collisionEngine
	merged map[string]renderer.AssembledFile
	nest   bool
}

func (e *collisionAssemblerEngine) BoundaryAssembly() string {
	if e.nest {
		return "nest"
	}
	return "merge"
}

func (e *collisionAssemblerEngine) AssembleBoundary(_ context.Context, _ renderer.BoundaryInfo) (map[string]renderer.AssembledFile, bool, error) {
	out := map[string]renderer.AssembledFile{}
	for k, v := range e.merged {
		out[k] = v
	}
	return out, e.nest, nil
}

// runCollisionRender drives the full pipeline over the named component
// instances, all in one boundary and all rendered by engine, and returns
// whatever the pipeline produced. Every instance renders through the same
// engine, so instances emitting one path is the collision under test.
func runCollisionRender(instances []string, engine renderer.Engine) (*renderer.RenderResult, error) {
	nodes := types.OrderedMap[types.IRNode]{}
	for _, id := range instances {
		nodes.Set(id, types.IRNode{Component: "core/api", BoundaryPath: "default"})
	}
	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "collide-app"},
			Spec: types.IRSpec{
				Nodes:      nodes,
				Provenance: types.IRProvenance{CompilerVersion: "test"},
			},
		},
		FeatureOrder:  ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{Aggregate: &types.Aggregate{}},
	}
	lookup := renderer.RecipeLookup{
		Recipe: func(_, _ string) (*types.Recipe, bool) {
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "collide-recipe", Version: "1.0.0"},
				Spec:     types.RecipeSpec{Capability: "collide", Source: "./chart"},
			}, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}
	factory := func(_ string) (renderer.Engine, error) { return engine, nil }
	return renderer.RenderAll(context.Background(), input, lookup, factory)
}

// TestRenderAll_FlatContributorsOnOneOutputPathHalt is the end-to-end proof that
// two contributors writing one output path stop the build instead of one
// silently overwriting the other, and that the diagnostic names both of them and
// the path.
func TestRenderAll_FlatContributorsOnOneOutputPathHalt(t *testing.T) {
	t.Parallel()

	engine := &collisionEngine{emit: []renderer.RenderedFile{
		{Path: "shared.yaml", Content: []byte("a: 1\n")},
	}}
	res, err := runCollisionRender([]string{"alpha", "beta"}, engine)
	if err == nil {
		t.Fatalf("expected a halt, got files: %v", sortedKeys(res.Files))
	}
	if !errors.Is(err, renderer.ErrAssembledOutputCollision()) {
		t.Fatalf("expected ErrAssembledOutputCollision, got: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{
		`shared.yaml" is written by component "alpha" and component "beta"`,
		"rename one of the colliding files so each output path is written once",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("diagnostic missing %q\ngot: %s", want, msg)
		}
	}
}

// TestRenderAll_CollisionDiagnosticIsOrderIndependent pins the property the
// guard would otherwise break. The maps feeding a dispatch group are plain Go
// maps whose iteration order is randomised per run, so a diagnostic naming
// "the first writer" would differ between runs of identical input. The message
// must be a function of the input alone.
func TestRenderAll_CollisionDiagnosticIsOrderIndependent(t *testing.T) {
	t.Parallel()

	engine := &collisionEngine{emit: []renderer.RenderedFile{
		{Path: "shared.yaml", Content: []byte("a: 1\n")},
	}}
	instances := []string{"alpha", "beta", "gamma"}

	var first string
	for i := 0; i < 40; i++ {
		_, err := runCollisionRender(instances, engine)
		if err == nil {
			t.Fatalf("iteration %d: expected a halt", i)
		}
		msg := err.Error()
		if i == 0 {
			first = msg
			continue
		}
		if msg != first {
			t.Fatalf("diagnostic changed between runs of identical input\nrun 0: %s\nrun %d: %s", first, i, msg)
		}
	}
	// Every contributor is named, in a fixed order, not just the two that
	// happened to be walked first.
	if !strings.Contains(first, `component "alpha", component "beta" and component "gamma"`) {
		t.Errorf("expected all three contributors named in order, got: %s", first)
	}
}

// TestRenderAll_CollisionAcrossManyPathsIsOrderIndependent pins the same
// property when more than one path collides: the paths are reported in a fixed
// order, so the message does not depend on which collision was noticed first.
func TestRenderAll_CollisionAcrossManyPathsIsOrderIndependent(t *testing.T) {
	t.Parallel()

	engine := &collisionEngine{emit: []renderer.RenderedFile{
		{Path: "zeta.yaml", Content: []byte("z: 1\n")},
		{Path: "alpha.yaml", Content: []byte("a: 1\n")},
		{Path: "mid.yaml", Content: []byte("m: 1\n")},
	}}
	var first string
	for i := 0; i < 40; i++ {
		_, err := runCollisionRender([]string{"one", "two"}, engine)
		if err == nil {
			t.Fatalf("iteration %d: expected a halt", i)
		}
		if i == 0 {
			first = err.Error()
			continue
		}
		if err.Error() != first {
			t.Fatalf("diagnostic changed between runs of identical input\nrun 0: %s\nrun %d: %s", first, i, err.Error())
		}
	}
	alpha := strings.Index(first, "alpha.yaml")
	mid := strings.Index(first, "mid.yaml")
	zeta := strings.Index(first, "zeta.yaml")
	if alpha < 0 || mid < 0 || zeta < 0 {
		t.Fatalf("expected all three colliding paths reported, got: %s", first)
	}
	if !(alpha < mid && mid < zeta) {
		t.Errorf("expected colliding paths reported in path order, got: %s", first)
	}
}

// TestRenderAll_AssemblyAndContributorCollisionNamesBoth covers the collision
// between an assembly's own output and a contributor's file nested beneath it,
// and pins the declared format appearing when the two disagree about it — the
// case where the format is the likeliest explanation of the mistake.
func TestRenderAll_AssemblyAndContributorCollisionNamesBoth(t *testing.T) {
	t.Parallel()

	engine := &collisionAssemblerEngine{
		collisionEngine: collisionEngine{emit: []renderer.RenderedFile{
			{Path: "shared.yaml", Content: []byte("a: 1\n")},
		}},
		merged: map[string]renderer.AssembledFile{
			"charts/solo/shared.yaml": {Content: []byte("merged: true\n"), Format: "yaml"},
		},
		nest: true,
	}
	_, err := runCollisionRender([]string{"solo"}, engine)
	if err == nil {
		t.Fatal("expected a halt")
	}
	if !errors.Is(err, renderer.ErrAssembledOutputCollision()) {
		t.Fatalf("expected ErrAssembledOutputCollision, got: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{
		`component "solo" (no declared format)`,
		`the "collide" assembly (declared format "yaml")`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("diagnostic missing %q\ngot: %s", want, msg)
		}
	}
}

// TestRenderAll_NestedContributorsWithOneFilenameDoNotCollide is the near-miss
// the guard must not fire on. Two contributors emit the same filename, and the
// assembly nests each under its own instance, so the two land on different
// output paths. Comparing what the contributors emitted rather than where it
// lands would reject this legitimate authoring.
func TestRenderAll_NestedContributorsWithOneFilenameDoNotCollide(t *testing.T) {
	t.Parallel()

	engine := &collisionAssemblerEngine{
		collisionEngine: collisionEngine{emit: []renderer.RenderedFile{
			{Path: "templates/deployment.yaml", Content: []byte("a: 1\n")},
		}},
		merged: map[string]renderer.AssembledFile{
			"Chart.yaml": {Content: []byte("name: collide\n")},
		},
		nest: true,
	}
	res, err := runCollisionRender([]string{"alpha", "beta"}, engine)
	if err != nil {
		t.Fatalf("near-miss must not halt: %v", err)
	}
	keys := sortedKeys(res.Files)
	for _, want := range []string{
		"charts/alpha/templates/deployment.yaml",
		"charts/beta/templates/deployment.yaml",
	} {
		found := false
		for _, k := range keys {
			if strings.HasSuffix(k, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("expected an output ending %q, got: %v", want, keys)
		}
	}
}

func sortedKeys(files map[string]renderer.AssembledFile) []string {
	out := make([]string, 0, len(files))
	for k := range files {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
