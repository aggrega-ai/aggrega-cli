// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// placementGraphFacts stands in for the presentation a build hands the render.
// Supplying one is what switches on the rules declared over assembled output —
// among them the refusal of two contributors writing one path. Without it these
// corpora would assemble a collision instead of halting on it, and the placement
// under test would then be proven against a weaker pipeline than a build runs.
type placementGraphFacts struct{ constraint.GraphView }

// placementHostedInput returns a render input whose named components all sit in
// ONE boundary and all carry the SAME attachment instance name, which is what
// puts their attachments in one dispatch group and so makes their output paths
// genuinely meet.
func placementHostedInput(hosts []string, instance, featureType string) renderer.RendererInput {
	input := testInput()

	componentLabels := types.OrderedMap[string]{}
	componentLabels.Set("component.aggrega.ai/type", "server")

	nodes := types.OrderedMap[types.IRNode]{}
	valuesMapping := types.OrderedMap[json.RawMessage]{}
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	order := ir.FeatureOrder{}
	for _, host := range hosts {
		nodes.Set(host, types.IRNode{
			Component: "core/compute/api-server", Labels: componentLabels,
			ResolvedVersion: "1.0.0", BoundaryPath: "local/default",
		})
		valuesMapping.Set(host, json.RawMessage(`{"port":8080}`))

		vals := types.OrderedMap[any]{}
		vals.Set("scrape_port", float64(9090))
		inner := types.OrderedMap[types.IRFeatureEntry]{}
		inner.Set(instance, types.IRFeatureEntry{Type: featureType, Values: vals})
		fvm.Set(host, inner)
		order[host] = []string{instance}
	}
	input.Graph.Spec.Nodes = nodes
	input.Graph.Spec.ValuesMapping = valuesMapping
	input.Graph.Spec.FeatureValuesMapping = fvm
	input.FeatureOrder = order
	input.GraphView = placementGraphFacts{}
	return input
}

// placementLookup binds every named component to a workload Recipe and the one
// feature type to the supplied FeatureRecipe.
func placementLookup(fr *types.FeatureRecipe, featureType string) renderer.RecipeLookup {
	return renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType != "core/compute/api-server" {
				return nil, false
			}
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe"},
				Spec: types.RecipeSpec{
					Capability: "helm",
					Implements: "core/compute/api-server",
					Workload: types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: myapp:latest
    ports:
      web: 8080
`},
				},
			}, true
		},
		FeatureRecipe: func(_, ft string) (*types.FeatureRecipe, bool) {
			if ft != featureType {
				return nil, false
			}
			return fr, true
		},
	}
}

// artifactsOnlyFR is a FeatureRecipe declaring artifacts: and nothing else. The
// artifact's output filename is the author's map key verbatim, so nothing in the
// emitted name separates one host's copy from another's — the placement is the
// only thing that can.
func artifactsOnlyFR(featureType string) *types.FeatureRecipe {
	art := types.OrderedMap[types.ArtifactEntry]{}
	art.Set("policy.yaml", types.ArtifactEntry{Inline: `apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: aux-policy
spec:
  podSelector: {}
`})
	return &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "aux-fr", Version: "1.0.0"},
		Spec: types.FeatureRecipeSpec{
			Capability: "helm",
			Implements: featureType,
			Artifacts:  art,
		},
	}
}

// TestRenderAll_ArtifactsAttachmentOnTwoComponentsPlacesUnderEachHost is the
// end-to-end proof that one instance name attached to two components in one
// boundary, bound to an artifacts-only FeatureRecipe, builds — because each
// attachment's output is placed under the component hosting it.
//
// One instance key on two components is ordinary authoring, and a cascade that
// mints one instance name across many components leaves an author no way to
// write anything else. An artifact's filename is the author's map key verbatim,
// so the emitted name cannot separate the two copies; the directory is what
// does, and it names the host.
func TestRenderAll_ArtifactsAttachmentOnTwoComponentsPlacesUnderEachHost(t *testing.T) {
	t.Parallel()

	const (
		instance    = "aux"
		featureType = "core/aux"
	)
	input := placementHostedInput([]string{"api", "worker"}, instance, featureType)

	result, err := renderer.RenderAll(context.Background(), input,
		placementLookup(artifactsOnlyFR(featureType), featureType), testHelmFactory(t))
	if err != nil {
		t.Fatalf("one instance name on two components must build, got: %v", err)
	}

	keys := fileKeys(result.Files)
	joined := strings.Join(keys, "\n")
	for _, host := range []string{"api", "worker"} {
		want := "features/" + host + "/" + instance + "/policy.yaml"
		if !strings.Contains(joined, want) {
			t.Errorf("no output key carries %q; the two attachments have not been placed under their hosts\nkeys: %v",
				want, keys)
		}
	}
}

// TestRenderAll_AttachmentPlacementIsOneShapeAcrossInputKinds pins the placement
// as a property of the attachment rather than of the input kind it renders from.
// Sourced, synthesised and artifacts output all land under
// features/<host>/<instance>/, so an author changing a FeatureRecipe's delivery
// mode does not find their output paths moved.
func TestRenderAll_AttachmentPlacementIsOneShapeAcrossInputKinds(t *testing.T) {
	t.Parallel()

	const (
		host        = "api"
		instance    = "aux"
		featureType = "core/aux"
		wantPrefix  = "features/" + host + "/" + instance + "/"
	)

	sourcedFR := func(t *testing.T) *types.FeatureRecipe {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"),
			[]byte("apiVersion: v2\nname: aux\nversion: 1.0.0\n"), 0o644); err != nil {
			t.Fatalf("write Chart.yaml: %v", err)
		}
		if err := os.Mkdir(filepath.Join(dir, "templates"), 0o755); err != nil {
			t.Fatalf("mkdir templates: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "templates", "configmap.yaml"),
			[]byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: aux-config\ndata:\n  marker: \"x\"\n"), 0o644); err != nil {
			t.Fatalf("write configmap.yaml: %v", err)
		}
		return &types.FeatureRecipe{
			Metadata: types.FeatureRecipeMetadata{Name: "aux-fr", Version: "1.0.0"},
			Spec: types.FeatureRecipeSpec{
				Capability:    "helm",
				Implements:    featureType,
				Source:        dir,
				ValuesMapping: types.ValuesMapping{Inline: `{}`},
			},
		}
	}

	workloadFR := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "aux-fr", Version: "1.0.0"},
		Spec: types.FeatureRecipeSpec{
			Capability: "helm",
			Implements: featureType,
			Workload: types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: prom/aux:v1
`},
		},
	}

	cases := []struct {
		kind string
		fr   func(t *testing.T) *types.FeatureRecipe
	}{
		{kind: "sourced", fr: sourcedFR},
		{kind: "workload", fr: func(*testing.T) *types.FeatureRecipe { return workloadFR }},
		{kind: "artifacts", fr: func(*testing.T) *types.FeatureRecipe { return artifactsOnlyFR(featureType) }},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.kind, func(t *testing.T) {
			t.Parallel()
			input := placementHostedInput([]string{host}, instance, featureType)
			result, err := renderer.RenderAll(context.Background(), input,
				placementLookup(tc.fr(t), featureType), testHelmFactory(t))
			if err != nil {
				t.Fatalf("%s attachment must render, got: %v", tc.kind, err)
			}
			var placed []string
			for _, k := range fileKeys(result.Files) {
				if strings.Contains(k, "features/") {
					placed = append(placed, k)
				}
			}
			sort.Strings(placed)
			if len(placed) == 0 {
				t.Fatalf("%s attachment emitted nothing under an attachment directory; keys: %v",
					tc.kind, fileKeys(result.Files))
			}
			for _, k := range placed {
				if !strings.Contains(k, wantPrefix) {
					t.Errorf("%s attachment output %q is not placed under %q", tc.kind, k, wantPrefix)
				}
			}
			t.Logf("%s attachment emitted: %v", tc.kind, placed)
		})
	}
}

// TestRenderAll_DottedHostPlacesAnUnescapedPathSegment pins that a host whose id
// carries a dot reaches a directory carrying that dot as written. A dot in a
// directory name is legal on every filesystem, so no escaping is applied to a
// path segment — the identity encoder writes label values, and a path segment is
// not one.
func TestRenderAll_DottedHostPlacesAnUnescapedPathSegment(t *testing.T) {
	t.Parallel()

	const (
		host        = "data.main-db"
		instance    = "aux"
		featureType = "core/aux"
	)
	input := placementHostedInput([]string{host}, instance, featureType)

	result, err := renderer.RenderAll(context.Background(), input,
		placementLookup(artifactsOnlyFR(featureType), featureType), testHelmFactory(t))
	if err != nil {
		t.Fatalf("a dotted host must render, got: %v", err)
	}

	want := "features/" + host + "/" + instance + "/policy.yaml"
	keys := fileKeys(result.Files)
	if !strings.Contains(strings.Join(keys, "\n"), want) {
		t.Errorf("no output key carries %q — a path segment must be written as authored\nkeys: %v", want, keys)
	}
	t.Logf("dotted host emitted: %v", keys)
}
