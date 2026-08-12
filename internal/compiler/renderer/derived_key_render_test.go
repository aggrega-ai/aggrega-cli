// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The names below are the shapes a provider actually derives, not toy strings: a
// logical id sanitised from a node id joined to a resource name, and a
// four-segment export name. The near-miss differs from the colliding name by one
// character, which is what makes the negative direction meaningful — a check
// matching on similarity rather than equality would reject it.
const (
	e2eDerivedLogicalID         = "paymentsapiQueue"
	e2eDerivedLogicalIDNearMiss = "paymentsapiQueve"
	e2eDerivedExportName        = "aggrega-prod-api-orders-db-host"
)

// declaringEngine is a synthetic provider engine that declares, per node, the
// names it derived — the same thing a real provider guest does when it returns
// them and the adapter forwards them to the node's sink. It emits a per-node
// output path so no output-path collision fires, keeping the derived-name check
// the only thing these tests can trip.
type declaringEngine struct {
	// declare maps a node id to the spaces and names that node declares.
	declare map[string]map[string][]string
}

func (e *declaringEngine) Render(_ context.Context, _ map[string]any, nodeCtx renderer.NodeRenderContext) ([]renderer.RenderedFile, error) {
	if spaces, ok := e.declare[nodeCtx.NodeID]; ok && nodeCtx.DerivedKeySink != nil {
		declared := types.OrderedMap[[]string]{}
		// Set in a fixed order so the payload this stub hands the host is a
		// function of the fixture rather than of Go's map iteration.
		for _, space := range []string{"logical-ids", "export-names"} {
			if keys, ok := spaces[space]; ok {
				declared.Set(space, keys)
			}
		}
		if declared.Len() > 0 {
			nodeCtx.DerivedKeySink(declared)
		}
	}
	return []renderer.RenderedFile{
		{Path: nodeCtx.NodeID + ".yaml", Content: []byte("id: " + nodeCtx.NodeID + "\n")},
	}, nil
}

func (e *declaringEngine) Contract() renderer.ProviderContract {
	return renderer.ProviderContract{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
}

// runDeclaringRender drives the full pipeline over the named component instances,
// all in one boundary and all rendered by engine.
func runDeclaringRender(instances []string, engine renderer.Provider) (*renderer.RenderResult, error) {
	nodes := types.OrderedMap[types.IRNode]{}
	for _, id := range instances {
		nodes.Set(id, types.IRNode{Component: "core/api", BoundaryPath: "default"})
	}
	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "declare-app"},
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
				Metadata: types.RecipeMetadata{Name: "declare-recipe", Version: "1.0.0"},
				Spec:     types.RecipeSpec{Capability: "declare", Source: "./chart"},
			}, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}
	factory := func(_ string) (renderer.Provider, error) { return engine, nil }
	return renderer.RenderAll(context.Background(), input, lookup, factory)
}

// TestRenderAll_TwoGuestsDeclaringOneNameHalt is the end-to-end proof in the
// positive direction: two contributors declaring one name in one key space stop
// the build, and the diagnostic names the space, the name and both contributors —
// enough to act on without opening the provider.
func TestRenderAll_TwoGuestsDeclaringOneNameHalt(t *testing.T) {
	t.Parallel()

	engine := &declaringEngine{declare: map[string]map[string][]string{
		"alpha": {"logical-ids": {e2eDerivedLogicalID}},
		"beta":  {"logical-ids": {e2eDerivedLogicalID}},
	}}
	res, err := runDeclaringRender([]string{"alpha", "beta"}, engine)
	if err == nil {
		t.Fatalf("expected a halt, got %d files", len(res.Files))
	}
	if !errors.Is(err, renderer.ErrDerivedKeyCollision()) {
		t.Fatalf("expected ErrDerivedKeyCollision, got: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{
		`in key space "logical-ids"`,
		`the name "paymentsapiQueue"`,
		`component "alpha" and component "beta"`,
		"change what one of them derives so each name is derived once within its space",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("diagnostic missing %q\ngot: %s", want, msg)
		}
	}
}

// TestRenderAll_TheSamePairWithoutTheCollisionRenders is the same pair in the
// negative direction: one character of one declared name differs, so nothing
// collides and the build completes. Both halves are needed — the positive
// direction alone would be satisfied by a guard that rejected every declaration.
func TestRenderAll_TheSamePairWithoutTheCollisionRenders(t *testing.T) {
	t.Parallel()

	engine := &declaringEngine{declare: map[string]map[string][]string{
		"alpha": {"logical-ids": {e2eDerivedLogicalID}},
		"beta":  {"logical-ids": {e2eDerivedLogicalIDNearMiss}},
	}}
	res, err := runDeclaringRender([]string{"alpha", "beta"}, engine)
	if err != nil {
		t.Fatalf("the same pair without a collision must render: %v", err)
	}
	if len(res.Files) == 0 {
		t.Fatal("expected rendered output")
	}
}

// TestRenderAll_DeclaredNamesDoNotChangeRenderedBytes is the byte-identity proof
// for the surface itself. The same inputs rendered with and without declarations
// attached must produce the same output, because declaring is an assertion about
// what the guest derived and never a change to what it emitted.
func TestRenderAll_DeclaredNamesDoNotChangeRenderedBytes(t *testing.T) {
	t.Parallel()

	instances := []string{"alpha", "beta"}
	silent := &declaringEngine{}
	declaring := &declaringEngine{declare: map[string]map[string][]string{
		"alpha": {"logical-ids": {e2eDerivedLogicalID}, "export-names": {e2eDerivedExportName}},
		"beta":  {"logical-ids": {e2eDerivedLogicalIDNearMiss}},
	}}

	withoutDecl, err := runDeclaringRender(instances, silent)
	if err != nil {
		t.Fatalf("render without declarations: %v", err)
	}
	withDecl, err := runDeclaringRender(instances, declaring)
	if err != nil {
		t.Fatalf("render with declarations: %v", err)
	}

	if len(withoutDecl.Files) != len(withDecl.Files) {
		t.Fatalf("file count changed: %d without declarations, %d with", len(withoutDecl.Files), len(withDecl.Files))
	}
	for path, want := range withoutDecl.Files {
		got, ok := withDecl.Files[path]
		if !ok {
			t.Errorf("output %q disappeared when declarations were attached", path)
			continue
		}
		if string(got.Content) != string(want.Content) {
			t.Errorf("output %q is not byte-identical with declarations attached", path)
		}
	}
}

// TestRenderAll_DerivedNameCollisionDiagnosticIsOrderIndependent pins the
// property end to end. The accumulating walks are not ordered by the input, so a
// diagnostic naming "the first declarer" would differ between runs of identical
// input.
func TestRenderAll_DerivedNameCollisionDiagnosticIsOrderIndependent(t *testing.T) {
	t.Parallel()

	engine := &declaringEngine{declare: map[string]map[string][]string{
		"alpha": {"logical-ids": {e2eDerivedLogicalID}},
		"beta":  {"logical-ids": {e2eDerivedLogicalID}},
		"gamma": {"logical-ids": {e2eDerivedLogicalID}},
	}}
	var first string
	for i := 0; i < 40; i++ {
		_, err := runDeclaringRender([]string{"alpha", "beta", "gamma"}, engine)
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
	if !strings.Contains(first, `component "alpha", component "beta" and component "gamma"`) {
		t.Errorf("expected all three contributors named in order, got: %s", first)
	}
}

// TestRenderAll_OneGuardCoversEverySpaceAGuestDeclares is what makes a second
// provider need no second guard. Two spaces the host knows nothing about are
// checked by the same code in one build, and both collisions are reported
// together rather than one build at a time.
func TestRenderAll_OneGuardCoversEverySpaceAGuestDeclares(t *testing.T) {
	t.Parallel()

	engine := &declaringEngine{declare: map[string]map[string][]string{
		"alpha": {"logical-ids": {e2eDerivedLogicalID}, "export-names": {e2eDerivedExportName}},
		"beta":  {"logical-ids": {e2eDerivedLogicalID}, "export-names": {e2eDerivedExportName}},
	}}
	_, err := runDeclaringRender([]string{"alpha", "beta"}, engine)
	if err == nil {
		t.Fatal("expected a halt")
	}
	if !errors.Is(err, renderer.ErrDerivedKeyCollision()) {
		t.Fatalf("expected ErrDerivedKeyCollision, got: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{
		`in key space "export-names" the name "aggrega-prod-api-orders-db-host"`,
		`in key space "logical-ids" the name "paymentsapiQueue"`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("diagnostic missing %q\ngot: %s", want, msg)
		}
	}
}
