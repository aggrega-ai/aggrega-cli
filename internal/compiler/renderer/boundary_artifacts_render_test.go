// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// Where the files a Recipe contributes to its boundary's shared output land, and
// how several contributors' declarations of one name meet, proved through the
// shipped terraform guest — the provider that claims to accept such files — and
// the render the build runs.

// boundaryArtifactsRootDir is the dispatch group's own directory for the fixture's
// one boundary: its declared directory, then the provider.
const boundaryArtifactsRootDir = "dist/cluster/terraform"

// providerConfig is a provider configuration Terraform accepts only at the root.
const providerConfig = "provider \"azurerm\" {\n  features {}\n}\n"

// boundaryArtifactsComponent is one component of the fixture: its name, and the
// boundary block its Recipe declares.
type boundaryArtifactsComponent struct {
	name   string
	shared map[string]string
	order  []string
}

// renderBoundaryArtifacts renders one boundary holding the components given, each
// bound to a Recipe of its own that writes one module file of its own and the
// boundary block the component carries, and returns the render's result or its
// refusal.
func renderBoundaryArtifacts(t *testing.T, components []boundaryArtifactsComponent) (*renderer.RenderResult, error) {
	t.Helper()
	const boundary = "cluster"

	nodes := types.OrderedMap[types.IRNode]{}
	clusterBoundary := types.Boundary{
		Directory:  "dist/cluster",
		Components: types.OrderedMap[types.BoundaryComponent]{},
		Boundaries: types.OrderedMap[types.Boundary]{},
	}
	infra := map[string]*resolver.BoundaryInfra{}
	recipes := map[string]*types.Recipe{}
	for _, c := range components {
		componentType := "core/" + c.name
		nodes.Set(c.name, types.IRNode{Component: componentType, BoundaryPath: boundary})
		clusterBoundary.Components.Set(c.name, types.BoundaryComponent{})
		infra[c.name] = &resolver.BoundaryInfra{BoundaryPath: boundary, Directory: "dist/cluster"}
		shared := types.OrderedMap[types.ArtifactEntry]{}
		for _, name := range c.order {
			shared.Set(name, types.ArtifactEntry{Inline: c.shared[name]})
		}
		recipes[componentType] = &types.Recipe{
			Metadata: types.RecipeMetadata{Name: c.name + "-recipe", Version: "1.0.0"},
			Spec: types.RecipeSpec{
				Capability: "terraform",
				Implements: componentType,
				Artifacts: oneInlineArtifact(c.name+".tf.json",
					"{\"output\":{\""+c.name+"_marker\":{\"value\":\""+c.name+"\"}}}\n"),
				Boundary: types.RecipeSpecBoundary{Artifacts: shared},
			},
		}
	}
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set(boundary, clusterBoundary)

	input := renderer.RendererInput{
		// The graph presentation a build always supplies. The rule refusing two
		// contributors on one output path reads the presentation of what the render
		// assembled, and a render handed no graph presentation builds none, so
		// without it the two collision cases below would render and pass nothing.
		GraphView: collisionGraphFacts{},
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "boundary-artifacts-app"},
			Spec: types.IRSpec{
				Nodes:         nodes,
				ValuesMapping: types.OrderedMap[json.RawMessage]{},
				InfraMapping:  types.OrderedMap[json.RawMessage]{},
				Provenance:    types.IRProvenance{CompilerVersion: "test"},
			},
		},
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "boundary-artifacts-app"}},
			Release:       &types.Release{Spec: types.ReleaseSpec{Boundaries: boundaries}},
			BoundaryInfra: infra,
		},
	}
	lookup := renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			r, ok := recipes[componentType]
			return r, ok
		},
	}
	return renderer.RenderAll(context.Background(), input, lookup, wasmEngineFactory(t))
}

// TestBoundaryArtifactsAreWrittenOnceInTheDispatchGroupsOwnDirectory places two
// components whose Recipes both declare one provider configuration identically,
// and one of them a second file of its own. The configuration is written once,
// beside the root configuration the guest assembled and in none of the modules,
// carrying the bytes declared and nothing stamped into it; the second file is
// written beside it; and each component's own artifact stays in its own module.
func TestBoundaryArtifactsAreWrittenOnceInTheDispatchGroupsOwnDirectory(t *testing.T) {
	t.Parallel()

	result, err := renderBoundaryArtifacts(t, []boundaryArtifactsComponent{
		{name: "api", shared: map[string]string{"providers.tf": providerConfig}, order: []string{"providers.tf"}},
		{name: "web", shared: map[string]string{
			"providers.tf": providerConfig,
			"versions.tf":  "terraform {\n  required_version = \">= 1.6\"\n}\n",
		}, order: []string{"versions.tf", "providers.tf"}},
	})
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	shared, ok := result.Files[boundaryArtifactsRootDir+"/providers.tf"]
	if !ok {
		t.Fatalf("no shared provider configuration beside the root configuration; emitted %v", sortedEmittedPaths(result.Files))
	}
	if string(shared.Content) != providerConfig {
		t.Errorf("the shared file carries %q; want the declared bytes %q, with nothing stamped into them", shared.Content, providerConfig)
	}
	if _, ok := result.Files[boundaryArtifactsRootDir+"/versions.tf"]; !ok {
		t.Errorf("the second shared file is not beside the root configuration; emitted %v", sortedEmittedPaths(result.Files))
	}
	if _, ok := result.Files[boundaryArtifactsRootDir+"/aggrega.modules.tf"]; !ok {
		t.Errorf("the root configuration is missing; emitted %v", sortedEmittedPaths(result.Files))
	}
	for p := range result.Files {
		if strings.HasSuffix(p, "/providers.tf") && p != boundaryArtifactsRootDir+"/providers.tf" {
			t.Errorf("a shared file was written inside a module as well, at %s", p)
		}
	}
	for _, own := range []string{"api/api.tf.json", "web/web.tf.json"} {
		if _, ok := result.Files[boundaryArtifactsRootDir+"/modules/"+own]; !ok {
			t.Errorf("the component's own artifact %s is not in its module; emitted %v", own, sortedEmittedPaths(result.Files))
		}
	}
}

// TestBoundaryArtifactsRenderByteIdentically renders the same input twice and
// compares every file, so the merge's order is the input's rather than an
// iteration's.
func TestBoundaryArtifactsRenderByteIdentically(t *testing.T) {
	t.Parallel()

	components := []boundaryArtifactsComponent{
		{name: "api", shared: map[string]string{"providers.tf": providerConfig, "b.tf": "# b\n", "a.tf": "# a\n"},
			order: []string{"providers.tf", "b.tf", "a.tf"}},
		{name: "web", shared: map[string]string{"providers.tf": providerConfig}, order: []string{"providers.tf"}},
	}
	first, err := renderBoundaryArtifacts(t, components)
	if err != nil {
		t.Fatalf("first RenderAll: %v", err)
	}
	second, err := renderBoundaryArtifacts(t, components)
	if err != nil {
		t.Fatalf("second RenderAll: %v", err)
	}
	if len(first.Files) != len(second.Files) {
		t.Fatalf("the two renders emitted %d and %d files", len(first.Files), len(second.Files))
	}
	for p, f := range first.Files {
		g, ok := second.Files[p]
		if !ok || !bytes.Equal(f.Content, g.Content) {
			t.Errorf("%s differs between two renders of identical input", p)
		}
	}
}

// TestBoundaryArtifactsDifferingUnderOneNameHalt declares one name with different
// bytes from two components: neither file is the one an author wrote for the
// boundary, so the build halts under the assembled-output collision, naming the
// file and both contributors.
func TestBoundaryArtifactsDifferingUnderOneNameHalt(t *testing.T) {
	t.Parallel()

	_, err := renderBoundaryArtifacts(t, []boundaryArtifactsComponent{
		{name: "api", shared: map[string]string{"providers.tf": providerConfig}, order: []string{"providers.tf"}},
		{name: "web", shared: map[string]string{"providers.tf": "provider \"azurerm\" {\n  features {}\n  skip_provider_registration = true\n}\n"},
			order: []string{"providers.tf"}},
	})
	if !errors.Is(err, renderer.ErrAssembledOutputCollision()) {
		t.Fatalf("got %v, want the assembled-output collision", err)
	}
	for _, want := range []string{boundaryArtifactsRootDir + "/providers.tf", "component api", "component web"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the halt does not name %q: %v", want, err)
		}
	}
}

// TestABoundaryArtifactNamedLikeTheAssembledRootHalts declares a shared file under
// the name the guest's own assembly writes the root configuration under. The two
// meet on one path, and the build halts under the same collision, naming the
// assembly as one of the contributors.
func TestABoundaryArtifactNamedLikeTheAssembledRootHalts(t *testing.T) {
	t.Parallel()

	_, err := renderBoundaryArtifacts(t, []boundaryArtifactsComponent{
		{name: "api", shared: map[string]string{"aggrega.modules.tf": "# not the root\n"}, order: []string{"aggrega.modules.tf"}},
	})
	if !errors.Is(err, renderer.ErrAssembledOutputCollision()) {
		t.Fatalf("got %v, want the assembled-output collision", err)
	}
	for _, want := range []string{boundaryArtifactsRootDir + "/aggrega.modules.tf", "component api", "the terraform assembly"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the halt does not name %q: %v", want, err)
		}
	}
}
