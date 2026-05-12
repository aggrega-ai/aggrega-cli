// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/internal/compiler/ir"
	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
	renderCompose "github.com/cognitio/aggrega-cli/internal/compiler/renderer/compose"
	"github.com/cognitio/aggrega-cli/pkg/resolver"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// composeFactory returns the compose engine for any provider name. Used by
// the boundary-scope FR proof cases (both variants exercise the compose
// provider end-to-end).
func composeFactory() renderer.EngineFactory {
	return func(string) (renderer.Engine, error) {
		return &renderCompose.ComposeEngine{}, nil
	}
}

// composeWorkloadRecipe builds a synthesised-mode compose Recipe — the
// inline `workload:` body is the YAML shape Phase B.2 unmarshal expects.
// SynthesizeWorkload at Phase B.4 produces the Intermediate=true fragment
// the per-(path, provider) compose AssembleBoundary then merges.
func composeWorkloadRecipe(name, image string, port int) *types.Recipe {
	body := `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: ` + image + `
    ports:
      p: ` + sprintInt(port) + `
services:
  ` + name + `:
    type: ClusterIP
    ports:
      p: ` + sprintInt(port) + `
`
	return &types.Recipe{
		Metadata: types.RecipeMetadata{Name: name + "-recipe", Version: "1.0.0"},
		Spec:     types.RecipeSpec{Provider: "compose", Workload: body},
	}
}

func sprintInt(n int) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = digits[n%10]
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// boundaryScopeBridgeFR builds a boundary-scope FeatureRecipe served by
// the compose engine in sourced mode. The compose engine requires a
// non-empty Source for non-Workload Render; the FR points at a temp
// compose fragment that declares the `aggrega-gw` service.
func boundaryScopeBridgeFR(t *testing.T) *types.FeatureRecipe {
	t.Helper()
	dir := t.TempDir()
	fragPath := filepath.Join(dir, "bridge.yaml")
	body := []byte(`services:
  aggrega-gw:
    image: nginx:alpine
    ports:
      - "15432:15432"
`)
	if err := os.WriteFile(fragPath, body, 0o644); err != nil {
		t.Fatalf("write bridge fragment: %v", err)
	}
	return &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "compose-bridge-fr", Version: "1.0.0"},
		Spec: types.FeatureRecipeSpec{
			Provider:       "compose",
			Implements:     "core/compose-bridge:1.0.0",
			ExecutionScope: "boundary",
			Source:         fragPath,
			ValuesMapping: types.ValuesMapping{Inline: `{}`},
		},
	}
}

// TestComposeBridge_SingleBoundary_Proof — variant A. One boundary holds
// `api` and `db` plus a `core/compose-bridge` boundary-scope FR. The FR
// runs once (deduped on (boundary_path, fr_instance)), and its synthesised
// `aggrega-gw` service merges with the api+db services into one
// docker-compose.yaml at the boundary's evaluated assembly path. Exercises:
//   - Pre-fix #1: the FR's evaluated path uses the FR-attached boundary's
//     authored `directory:` (not the orchestrator's nil-pass default).
//   - Phase B.6 dedup-by-(boundary_path, fr_instance).
//   - Compose AssembleBoundary merging per-component fragments + boundary-FR
//     fragment at one (path, provider) tuple.
func TestComposeBridge_SingleBoundary_Proof(t *testing.T) {
	t.Parallel()

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/api", BoundaryPath: "cluster"})
	nodes.Set("db", types.IRNode{Component: "core/db", BoundaryPath: "cluster"})

	// Boundary-attached compose-bridge feature.
	boundaryFeatures := types.OrderedMap[any]{}
	boundaryFeatures.Set("bridge", map[string]any{
		"type": "core/compose-bridge:1.0.0",
		"infra": map[string]any{
			"mappedPort": 15432,
		},
	})
	clusterBoundary := types.Boundary{
		Directory:  "dist/cluster",
		Features:   boundaryFeatures,
		Components: types.OrderedMap[types.BoundaryComponent]{},
		Boundaries: types.OrderedMap[types.Boundary]{},
	}
	clusterBoundary.Components.Set("api", types.BoundaryComponent{})
	clusterBoundary.Components.Set("db", types.BoundaryComponent{})
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set("cluster", clusterBoundary)

	bi := map[string]*resolver.BoundaryInfra{
		"api": {BoundaryPath: "cluster", Directory: "dist/cluster"},
		"db":  {BoundaryPath: "cluster", Directory: "dist/cluster"},
	}

	// FeatureValuesMapping: every component in the boundary's subtree
	// inherits the boundary-attached `bridge` feature (the IR builder's
	// Tier-4 propagation). Phase B.6 reads from here per-component;
	// dedup-on-(boundary_path, fr_instance) collapses to one dispatch.
	bridgeEntry := types.IRFeatureEntry{Type: "core/compose-bridge:1.0.0"}
	apiFV := types.OrderedMap[types.IRFeatureEntry]{}
	apiFV.Set("bridge", bridgeEntry)
	dbFV := types.OrderedMap[types.IRFeatureEntry]{}
	dbFV.Set("bridge", bridgeEntry)
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set("api", apiFV)
	fvm.Set("db", dbFV)

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "compose-bridge-app"},
			Spec: types.IRSpec{
				Nodes:                nodes,
				ValuesMapping:        types.OrderedMap[json.RawMessage]{},
				InfraMapping:         types.OrderedMap[json.RawMessage]{},
				FeatureValuesMapping: fvm,
				Provenance:           types.IRProvenance{CompilerVersion: "test"},
			},
		},
		FeatureOrder: ir.FeatureOrder{
			"api": []string{"bridge"},
			"db":  []string{"bridge"},
		},
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "compose-bridge-app"}},
			Release:       &types.Release{Spec: types.ReleaseSpec{Boundaries: boundaries}},
			BoundaryInfra: bi,
		},
	}

	apiRecipe := composeWorkloadRecipe("api", "api:1.0", 8080)
	dbRecipe := composeWorkloadRecipe("db", "postgres:15", 5432)
	bridgeFR := boundaryScopeBridgeFR(t)
	lookup := renderer.RecipeLookup{
		Recipe: func(componentType string) (*types.Recipe, bool) {
			switch componentType {
			case "core/api":
				return apiRecipe, true
			case "core/db":
				return dbRecipe, true
			}
			return nil, false
		},
		FeatureRecipe: func(featureType string) (*types.FeatureRecipe, bool) {
			if strings.HasPrefix(featureType, "core/compose-bridge") {
				return bridgeFR, true
			}
			return nil, false
		},
	}

	result, err := renderer.RenderAll(context.Background(), input, lookup, composeFactory())
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	// Pre-fix #1: the FR's authored directory: must be honored. Output
	// should land under dist/cluster/compose/, not boundaries/cluster/...
	wantPath := "dist/cluster/compose/docker-compose.yaml"
	composeBody, ok := result.Files[wantPath]
	if !ok {
		paths := make([]string, 0, len(result.Files))
		for p := range result.Files {
			paths = append(paths, p)
		}
		t.Fatalf("expected merged compose at %q; got paths %v", wantPath, paths)
	}

	body := string(composeBody)
	// All three services must be present in one merged compose file:
	// api + db (per-component) + aggrega-gw (boundary-scope FR).
	for _, svcName := range []string{"api:", "db:", "aggrega-gw:"} {
		if !strings.Contains(body, svcName) {
			t.Errorf("merged docker-compose.yaml missing service %q\n--- body ---\n%s", svcName, body)
		}
	}
	// The bridge service uses the FR's image.
	if !strings.Contains(body, "nginx:alpine") {
		t.Errorf("expected nginx:alpine (bridge image) in merged compose:\n%s", body)
	}
}

// TestComposeBridge_MergedPath_Proof — variant B. Parent boundary "cluster"
// holds the `core/compose-bridge` FR (no direct components); two child
// boundaries `services-net` and `data-net` declare
// `directory: "{{ .parent_boundary.directory }}"` and host api / db
// respectively. All contributors merge at the parent's evaluated path,
// producing one docker-compose.yaml. Exercises:
//   - Pre-fix #2: parent_boundary.directory cascade — child boundaries'
//     directory: templates evaluate to the parent's directory.
//   - Pre-fix #1: the FR's path eval uses the parent boundary's authored
//     directory: even though the parent has no direct components.
//   - Per-(path, provider) merge: children's compose contributors and the
//     parent's boundary-scope FR contributor land at one assembly path.
func TestComposeBridge_MergedPath_Proof(t *testing.T) {
	t.Parallel()

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/api", BoundaryPath: "cluster/services-net"})
	nodes.Set("db", types.IRNode{Component: "core/db", BoundaryPath: "cluster/data-net"})

	// Parent boundary holds the bridge FR (no direct components).
	clusterFeatures := types.OrderedMap[any]{}
	clusterFeatures.Set("bridge", map[string]any{
		"type":  "core/compose-bridge:1.0.0",
		"infra": map[string]any{"mappedPort": 15432},
	})

	servicesNet := types.Boundary{
		Directory:  "{{ .parent_boundary.directory }}",
		Components: types.OrderedMap[types.BoundaryComponent]{},
	}
	servicesNet.Components.Set("api", types.BoundaryComponent{})

	dataNet := types.Boundary{
		Directory:  "{{ .parent_boundary.directory }}",
		Components: types.OrderedMap[types.BoundaryComponent]{},
	}
	dataNet.Components.Set("db", types.BoundaryComponent{})

	clusterBoundary := types.Boundary{
		Directory:  "dist/cluster",
		Features:   clusterFeatures,
		Components: types.OrderedMap[types.BoundaryComponent]{},
		Boundaries: types.OrderedMap[types.Boundary]{},
	}
	clusterBoundary.Boundaries.Set("services-net", servicesNet)
	clusterBoundary.Boundaries.Set("data-net", dataNet)

	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set("cluster", clusterBoundary)

	// Per-component BoundaryInfra carries each component's CONTAINING
	// boundary directory: template (the child boundaries declare
	// "{{ .parent_boundary.directory }}"). The orchestrator pre-computes
	// the parent's evaluated directory, then evaluates the children's
	// templates against it — both children land at "dist/cluster".
	bi := map[string]*resolver.BoundaryInfra{
		"api": {BoundaryPath: "cluster/services-net", Directory: "{{ .parent_boundary.directory }}"},
		"db":  {BoundaryPath: "cluster/data-net", Directory: "{{ .parent_boundary.directory }}"},
	}

	// Boundary-attached bridge feature propagates to every node in the
	// subtree under cluster (api in services-net, db in data-net).
	bridgeEntry := types.IRFeatureEntry{Type: "core/compose-bridge:1.0.0"}
	apiFV := types.OrderedMap[types.IRFeatureEntry]{}
	apiFV.Set("bridge", bridgeEntry)
	dbFV := types.OrderedMap[types.IRFeatureEntry]{}
	dbFV.Set("bridge", bridgeEntry)
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set("api", apiFV)
	fvm.Set("db", dbFV)

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "compose-bridge-app"},
			Spec: types.IRSpec{
				Nodes:                nodes,
				ValuesMapping:        types.OrderedMap[json.RawMessage]{},
				InfraMapping:         types.OrderedMap[json.RawMessage]{},
				FeatureValuesMapping: fvm,
				Provenance:           types.IRProvenance{CompilerVersion: "test"},
			},
		},
		FeatureOrder: ir.FeatureOrder{
			"api": []string{"bridge"},
			"db":  []string{"bridge"},
		},
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "compose-bridge-app"}},
			Release:       &types.Release{Spec: types.ReleaseSpec{Boundaries: boundaries}},
			BoundaryInfra: bi,
		},
	}

	apiRecipe := composeWorkloadRecipe("api", "api:1.0", 8080)
	dbRecipe := composeWorkloadRecipe("db", "postgres:15", 5432)
	bridgeFR := boundaryScopeBridgeFR(t)
	lookup := renderer.RecipeLookup{
		Recipe: func(componentType string) (*types.Recipe, bool) {
			switch componentType {
			case "core/api":
				return apiRecipe, true
			case "core/db":
				return dbRecipe, true
			}
			return nil, false
		},
		FeatureRecipe: func(featureType string) (*types.FeatureRecipe, bool) {
			if strings.HasPrefix(featureType, "core/compose-bridge") {
				return bridgeFR, true
			}
			return nil, false
		},
	}

	result, err := renderer.RenderAll(context.Background(), input, lookup, composeFactory())
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	// Both child boundaries' contributors AND the parent's boundary-scope
	// FR contribution merge at the parent's evaluated path
	// "dist/cluster/compose/" (default subDirectory: "{{ .provider }}").
	wantPath := "dist/cluster/compose/docker-compose.yaml"
	composeBody, ok := result.Files[wantPath]
	if !ok {
		paths := make([]string, 0, len(result.Files))
		for p := range result.Files {
			paths = append(paths, p)
		}
		t.Fatalf("expected merged compose at %q; got paths %v", wantPath, paths)
	}

	body := string(composeBody)
	for _, svcName := range []string{"api:", "db:", "aggrega-gw:"} {
		if !strings.Contains(body, svcName) {
			t.Errorf("merged docker-compose.yaml missing service %q\n--- body ---\n%s", svcName, body)
		}
	}
	if !strings.Contains(body, "nginx:alpine") {
		t.Errorf("expected nginx:alpine (bridge image) in merged compose:\n%s", body)
	}
}
